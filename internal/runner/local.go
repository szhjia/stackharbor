package runner

import (
	"context"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/execenv"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type handle struct {
	reader          process.Reader
	captureMu       sync.Mutex
	lastTopology    uint64
	journal         Journal
	launchID        string
	persisted       []model.ProcessIdentity
	journalFinished bool
	journalRecovery bool
	rootIdentity    model.ProcessIdentity
	groupProbe      func(int, syscall.Signal) error
	cmd             *exec.Cmd
	mu              sync.Mutex
	stopMu          sync.Mutex
	owned           map[int32]model.ProcessIdentity
	done            chan ExitResult
	finished        chan struct{}
	rootExited      bool
	policy          model.StopPolicy
	signal          func(int, syscall.Signal) error
}

func (l *local) Start(ctx context.Context, s model.Service, output func(string, string)) (Handle, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if l.managed && l.journal == nil {
		return nil, fmt.Errorf("managed runner requires a launch journal")
	}
	if len(s.Command) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	// Discovering descendants is required for bounded, ownership-safe cleanup.
	// Refuse to launch anything when that prerequisite is already unavailable.
	var topologyErr error
	if l.managed {
		_, topologyErr = process.FreshTopology(ctx, l.reader)
	} else {
		_, topologyErr = l.reader.List(ctx)
	}
	if topologyErr != nil {
		return nil, fmt.Errorf("cannot inspect process tree; service not started (allow process inspection and ensure ps is available): %w", topologyErr)
	}
	env := map[string]string{}
	if !s.EnvFrozen {
		for _, e := range os.Environ() {
			k, v, ok := strings.Cut(e, "=")
			if ok {
				env[k] = v
			}
		}
	}
	for k, v := range s.Env {
		env[k] = v
	}
	environment := make([]string, 0, len(env))
	for k, v := range env {
		environment = append(environment, k+"="+v)
	}
	sort.Strings(environment)
	cmd := execenv.CommandContext(context.Background(), environment, s.Cwd, s.Command[0], s.Command[1:]...)
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	var release *os.File
	launchID := ""
	if l.journal != nil {
		var err error
		launchID, err = l.journal.Intent(s)
		if err != nil {
			return nil, fmt.Errorf("cannot persist launch intent: %w", err)
		}
		executable, err := os.Executable()
		if err != nil {
			return nil, l.abortIntent(launchID, err)
		}
		read, write, err := os.Pipe()
		if err != nil {
			return nil, l.abortIntent(launchID, err)
		}
		release = write
		defer release.Close()
		defer read.Close()
		target := cmd.Path
		cmd = exec.Command(executable, append([]string{execGateMode, target}, s.Command...)...)
		cmd.Dir = s.Cwd
		cmd.Env = environment
		cmd.ExtraFiles = []*os.File{read}
	}
	cmd.WaitDelay = time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	h := &handle{reader: l.reader, journal: l.journal, launchID: launchID, cmd: cmd, owned: map[int32]model.ProcessIdentity{}, done: make(chan ExitResult, 1), finished: make(chan struct{}), policy: s.Stop, signal: syscall.Kill}
	var wg sync.WaitGroup
	for _, stream := range []string{"stdout", "stderr"} {
		r, w := io.Pipe()
		if stream == "stdout" {
			cmd.Stdout = w
		} else {
			cmd.Stderr = w
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer r.Close()
			logs.Consume(context.Background(), r, func(line string) {
				if output != nil {
					output(stream, line)
				}
			})
		}()
		defer func() {
			if cmd.Process == nil {
				w.Close()
			}
		}()
	}
	if err := cmd.Start(); err != nil {
		return nil, l.abortIntent(launchID, err)
	}
	info, err := process.ReadIdentity(context.Background(), l.reader, int32(cmd.Process.Pid))
	if err == nil && l.managed && (info.Identity.PID != int32(cmd.Process.Pid) || info.Identity.CreatedMillis <= 0 || info.PGID != int32(cmd.Process.Pid)) {
		err = fmt.Errorf("invalid process identity")
	}
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		cmd.Stdout.(*io.PipeWriter).Close()
		cmd.Stderr.(*io.PipeWriter).Close()
		wg.Wait()
		return nil, l.abortIntent(launchID, fmt.Errorf("cannot establish process identity: %w", err))
	}
	h.owned[info.Identity.PID] = info.Identity
	if l.journal != nil {
		h.rootIdentity = info.Identity
		h.groupProbe = syscall.Kill
		if err := l.journal.Launched(launchID, info.Identity); err != nil {
			release.Close()
			cmd.Process.Kill()
			cmd.Wait()
			cmd.Stdout.(*io.PipeWriter).Close()
			cmd.Stderr.(*io.PipeWriter).Close()
			wg.Wait()
			return nil, l.abortIntent(launchID, fmt.Errorf("cannot persist launched identity: %w", err))
		}
		h.persisted = []model.ProcessIdentity{info.Identity}
		if err := ctx.Err(); err != nil {
			release.Close()
			cmd.Process.Kill()
			cmd.Wait()
			cmd.Stdout.(*io.PipeWriter).Close()
			cmd.Stderr.(*io.PipeWriter).Close()
			wg.Wait()
			return nil, l.abortIntent(launchID, err)
		}
		if _, err := release.Write([]byte{1}); err != nil {
			release.Close()
			cmd.Process.Kill()
			cmd.Wait()
			cmd.Stdout.(*io.PipeWriter).Close()
			cmd.Stderr.(*io.PipeWriter).Close()
			wg.Wait()
			return nil, l.abortIntent(launchID, fmt.Errorf("cannot release exec gate: %w", err))
		}
		release.Close()
		process.InvalidateTopology(l.reader)
	} else {
		h.capture(context.Background())
	}
	go h.watch(ctx)
	go func() {
		err := cmd.Wait()
		cmd.Stdout.(*io.PipeWriter).Close()
		cmd.Stderr.(*io.PipeWriter).Close()
		wg.Wait()
		h.mu.Lock()
		h.rootExited = true
		h.mu.Unlock()
		cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result := h.Stop(cleanCtx, s.Stop)
		if !result.Complete {
			err = fmt.Errorf("cleanup incomplete: %v", result.Errors)
		}
		code := cmd.ProcessState.ExitCode()
		if code == 0 && result.Complete && errors.Is(err, exec.ErrWaitDelay) {
			err = nil
		}
		h.done <- ExitResult{Code: code, Err: err}
		close(h.done)
		close(h.finished)
	}()
	return h, nil
}
func (h *handle) Done() <-chan ExitResult { return h.done }
func (h *handle) Identities() []model.ProcessIdentity {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []model.ProcessIdentity{}
	for _, id := range h.owned {
		out = append(out, id)
	}
	return out
}
func (h *handle) watch(ctx context.Context) {
	if h.journal != nil {
		h.watchManaged(ctx)
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := h.capture(context.Background()); err != nil && h.journal != nil {
				c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				h.Stop(c, h.policy)
				cancel()
				return
			}
		case <-ctx.Done():
			c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			h.Stop(c, h.policy)
			cancel()
			return
		case <-h.finished:
			return
		}
	}
}

func (l *local) abortIntent(id string, cause error) error {
	if l.journal != nil && id != "" {
		if err := l.journal.Finished(id, true); err != nil {
			return errors.Join(cause, fmt.Errorf("cannot finish launch journal: %w", err))
		}
	}
	return cause
}

// Managed handles wait on shared generations instead of waking every100ms
// only to discover the ownership cache is unchanged. Launch and cancellation
// still wake promptly; actual stop/signal checks always bypass the cache.
func (h *handle) watchManaged(ctx context.Context) {
	initial := time.NewTimer(100 * time.Millisecond)
	defer initial.Stop()
	select {
	case <-ctx.Done():
		h.stopFromWatch()
		return
	case <-h.finished:
		return
	case <-initial.C:
	}
	for {
		if ctx.Err() != nil {
			h.stopFromWatch()
			return
		}
		select {
		case <-h.finished:
			return
		default:
		}
		if err := h.capture(context.Background()); err != nil {
			h.stopFromWatch()
			return
		}
		h.captureMu.Lock()
		revision := h.lastTopology
		h.captureMu.Unlock()
		if !process.WaitTopology(ctx, h.reader, revision, h.finished) {
			if ctx.Err() != nil {
				h.stopFromWatch()
			}
			return
		}
	}
}
func (h *handle) stopFromWatch() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.Stop(ctx, h.policy)
}
