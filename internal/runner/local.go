package runner

import (
	"context"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type handle struct {
	reader     process.Reader
	cmd        *exec.Cmd
	mu         sync.Mutex
	stopMu     sync.Mutex
	owned      map[int32]model.ProcessIdentity
	done       chan ExitResult
	finished   chan struct{}
	rootExited bool
	policy     model.StopPolicy
	signal     func(int, syscall.Signal) error
}

func (l *local) Start(ctx context.Context, s model.Service, output func(string, string)) (Handle, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(s.Command) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	cmd := exec.Command(s.Command[0], s.Command[1:]...)
	cmd.WaitDelay = time.Second
	cmd.Dir = s.Cwd
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
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	h := &handle{reader: l.reader, cmd: cmd, owned: map[int32]model.ProcessIdentity{}, done: make(chan ExitResult, 1), finished: make(chan struct{}), policy: s.Stop, signal: syscall.Kill}
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
		return nil, err
	}
	info, err := l.reader.Read(context.Background(), int32(cmd.Process.Pid))
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		cmd.Stdout.(*io.PipeWriter).Close()
		cmd.Stderr.(*io.PipeWriter).Close()
		wg.Wait()
		return nil, fmt.Errorf("cannot establish process identity: %w", err)
	}
	h.owned[info.Identity.PID] = info.Identity
	h.capture(context.Background())
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
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.capture(context.Background())
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
