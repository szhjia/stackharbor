package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
)

func TestMain(m *testing.M) {
	if handled, code := RunExecGate(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

type memoryJournal struct {
	mu                                              sync.Mutex
	events                                          []string
	finishes                                        []bool
	identities                                      []model.ProcessIdentity
	intentErr, launchedErr, updatedErr, finishedErr error
	onLaunched                                      func(model.ProcessIdentity)
}

func (j *memoryJournal) Intent(model.Service) (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, "intent")
	return "launch", j.intentErr
}
func (j *memoryJournal) Launched(_ string, id model.ProcessIdentity) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, "launched")
	j.identities = []model.ProcessIdentity{id}
	if j.onLaunched != nil {
		j.onLaunched(id)
	}
	return j.launchedErr
}
func (j *memoryJournal) Updated(_ string, ids []model.ProcessIdentity) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, "updated")
	j.identities = append([]model.ProcessIdentity(nil), ids...)
	return j.updatedErr
}
func (j *memoryJournal) Finished(_ string, complete bool) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.finishes = append(j.finishes, complete)
	if complete {
		j.events = append(j.events, "finished")
		j.identities = nil
	} else {
		j.events = append(j.events, "recovery_required")
	}
	return j.finishedErr
}
func TestExecGateRejectsParentEOFAndInvalidRelease(t *testing.T) {
	for _, release := range [][]byte{nil, {0}} {
		marker := filepath.Join(t.TempDir(), "must-not-exist")
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], execGateMode, "/bin/sh", "sh", "-c", `printf unsafe > "$1"`, "fixture", marker)
		cmd.ExtraFiles = []*os.File{read}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		read.Close()
		if release != nil {
			write.Write(release)
		}
		write.Close()
		err = cmd.Wait()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 125 {
			t.Fatalf("gate exit: %v", err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("unauthorized command ran: %v", err)
		}
	}
}
func TestManagedJournalFailuresDoNotRunCommand(t *testing.T) {
	for _, stage := range []string{"intent", "launched", "canceled"} {
		t.Run(stage, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "must-not-exist")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			journal := &memoryJournal{}
			switch stage {
			case "intent":
				journal.intentErr = errors.New("disk failed")
			case "launched":
				journal.launchedErr = errors.New("disk failed")
			case "canceled":
				journal.onLaunched = func(model.ProcessIdentity) { cancel() }
			}
			handle, err := NewManaged(process.NewReader(), journal).Start(ctx, model.Service{Command: []string{"/bin/sh", "-c", `printf unsafe > "$1"`, "fixture", marker}}, nil)
			if err == nil || handle != nil {
				t.Fatalf("did not abort: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("command ran before durable authorization: %v", err)
			}
		})
	}
}
func TestManagedGatePersistsIdentityBeforeExecutingFrozenPATH(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "workspace-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf '%s:%s:%s' \"$ONLY_VALUE\" \"$PATH\" \"$$\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var durablePID int32
	journal := &memoryJournal{onLaunched: func(id model.ProcessIdentity) {
		durablePID = id.PID
		if id.PID <= 0 || id.CreatedMillis <= 0 {
			t.Errorf("invalid durable identity: %+v", id)
		}
	}}
	var output strings.Builder
	var outputMu sync.Mutex
	handle, err := NewManaged(process.NewSharedReader(process.NewReader(), func() time.Duration { return time.Second }), journal).Start(context.Background(), model.Service{Command: []string{"workspace-tool"}, Cwd: root, EnvFrozen: true, Env: map[string]string{"PATH": ".", "ONLY_VALUE": "frozen"}}, func(_, line string) { outputMu.Lock(); output.WriteString(line); outputMu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-handle.Done():
		if result.Err != nil || result.Code != 0 {
			t.Fatalf("result: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("managed command did not exit")
	}
	outputMu.Lock()
	got := output.String()
	outputMu.Unlock()
	if got != "frozen:.:"+strconv.Itoa(int(durablePID)) {
		t.Fatalf("wrong environment/toolchain: %q", got)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.events) < 3 || journal.events[0] != "intent" || journal.events[1] != "launched" || journal.events[len(journal.events)-1] != "finished" || len(journal.identities) != 0 {
		t.Fatalf("journal ordering/cleanup: %+v ids=%+v", journal.events, journal.identities)
	}
}

func TestVerifiedSignalAlwaysBypassesSharedIdentityCache(t *testing.T) {
	reader := &sharedSignalReader{identity: model.ProcessIdentity{PID: 10, CreatedMillis: 1}}
	cached := process.NewSharedReader(reader, func() time.Duration { return time.Hour })
	old, _ := cached.Read(context.Background(), 10)
	reader.identity.CreatedMillis++
	called := false
	err := verifiedSignal(context.Background(), cached, old.Identity, func(int) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("cached PID reuse check signaled: %v", err)
	}
}

type sharedSignalReader struct {
	identity model.ProcessIdentity
	reads    int
}

func (r *sharedSignalReader) List(context.Context) ([]process.Info, error) {
	return []process.Info{{Identity: r.identity}}, nil
}
func (r *sharedSignalReader) Read(context.Context, int32) (process.Info, error) {
	r.reads++
	return process.Info{Identity: r.identity}, nil
}

func TestCaptureJournalWritesOnlyChangedOwnership(t *testing.T) {
	id := model.ProcessIdentity{PID: 10, CreatedMillis: 1}
	journal := &memoryJournal{}
	handle := &handle{reader: &sharedSignalReader{identity: id}, journal: journal, launchID: "test", persisted: []model.ProcessIdentity{id}, owned: map[int32]model.ProcessIdentity{id.PID: id}}
	for range 3 {
		if err := handle.capture(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(journal.events) != 0 {
		t.Fatalf("unchanged ownership persisted: %v", journal.events)
	}
	handle.owned = map[int32]model.ProcessIdentity{}
	journal.updatedErr = errors.New("disk failed")
	if err := handle.capture(context.Background()); err == nil {
		t.Fatal("journal write failure was ignored")
	}
}

type changingTopologyReader struct{ infos map[int32]process.Info }

func (r *changingTopologyReader) List(context.Context) ([]process.Info, error) {
	all := []process.Info{}
	for _, info := range r.infos {
		all = append(all, info)
	}
	return all, nil
}
func (r *changingTopologyReader) Read(_ context.Context, pid int32) (process.Info, error) {
	if info, ok := r.infos[pid]; ok {
		return info, nil
	}
	return process.Info{}, os.ErrNotExist
}
func TestStopRefreshesTopologyAndSignalsNewDescendant(t *testing.T) {
	root := model.ProcessIdentity{PID: 10, CreatedMillis: 1}
	child := model.ProcessIdentity{PID: 11, CreatedMillis: 2}
	base := &changingTopologyReader{infos: map[int32]process.Info{10: {Identity: root}}}
	cached := process.NewSharedReader(base, func() time.Duration { return time.Hour })
	cached.List(context.Background())
	cached.Read(context.Background(), 10)
	base.infos[11] = process.Info{Identity: child, PPID: 10}
	signalled := map[int32]bool{}
	handle := &handle{reader: cached, owned: map[int32]model.ProcessIdentity{root.PID: root}, signal: func(pid int, _ syscall.Signal) error {
		signalled[int32(pid)] = true
		delete(base.infos, int32(pid))
		return nil
	}}
	result := handle.Stop(context.Background(), model.StopPolicy{Timeout: time.Second})
	if !result.Complete || !signalled[root.PID] || !signalled[child.PID] {
		t.Fatalf("stale topology skipped child: %+v signals=%v", result, signalled)
	}
}
func TestManagedRootExitCleansTrackedDetachedChild(t *testing.T) {
	journal := &memoryJournal{}
	handle, err := NewManaged(process.NewSharedReader(process.NewReader(), func() time.Duration { return time.Second }), journal).Start(context.Background(), model.Service{Command: []string{os.Args[0], "-test.run=TestRunnerFixture"}, Env: map[string]string{"SH_FIXTURE": "root-exit"}, Stop: model.StopPolicy{Timeout: time.Second}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		handle.Stop(ctx, model.StopPolicy{Timeout: time.Second})
	})
	select {
	case result := <-handle.Done():
		if result.Err != nil {
			t.Fatalf("detached descendant cleanup: %+v", result)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("managed root cleanup timed out")
	}
	if len(handle.Identities()) != 0 {
		t.Fatalf("tracked identities remain: %+v", handle.Identities())
	}
}

func TestManagedCaptureNeverAdoptsChildrenThroughCachedReusedParent(t *testing.T) {
	oldRoot := model.ProcessIdentity{PID: 10, CreatedMillis: 1}
	newRoot := model.ProcessIdentity{PID: 10, CreatedMillis: 2}
	child := model.ProcessIdentity{PID: 11, CreatedMillis: 3}
	base := &changingTopologyReader{infos: map[int32]process.Info{10: {Identity: oldRoot}, 11: {Identity: child, PPID: 10}}}
	cached := process.NewSharedReader(base, func() time.Duration { return time.Hour })
	cached.List(context.Background())
	cached.Read(context.Background(), 10)
	base.infos[10] = process.Info{Identity: newRoot}
	handle := &handle{reader: cached, journal: &memoryJournal{}, launchID: "test", persisted: []model.ProcessIdentity{oldRoot}, owned: map[int32]model.ProcessIdentity{oldRoot.PID: oldRoot}}
	if err := handle.capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(handle.Identities()) != 0 {
		t.Fatalf("adopted unrelated child of reused parent: %+v", handle.Identities())
	}
}
func TestManagedCaptureSkipsUnchangedTopologyGeneration(t *testing.T) {
	root := model.ProcessIdentity{PID: 10, CreatedMillis: 1}
	base := &sharedSignalReader{identity: root}
	cached := process.NewSharedReader(base, func() time.Duration { return time.Hour })
	handle := &handle{reader: cached, journal: &memoryJournal{}, launchID: "test", persisted: []model.ProcessIdentity{root}, owned: map[int32]model.ProcessIdentity{root.PID: root}}
	for range 10 {
		if err := handle.capture(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if base.reads != 1 {
		t.Fatalf("unchanged100ms ticks repeated identity work %d times", base.reads)
	}
}

func TestManagedEmptyOwnershipRequiresEmptyLaunchGroup(t *testing.T) {
	for _, groupErr := range []error{nil, syscall.EPERM, syscall.EINVAL} {
		t.Run(fmt.Sprint(groupErr), func(t *testing.T) {
			root := model.ProcessIdentity{PID: 12345, CreatedMillis: 1}
			journal := &memoryJournal{identities: []model.ProcessIdentity{root}}
			h := &handle{reader: &changingTopologyReader{infos: map[int32]process.Info{}}, journal: journal, launchID: "test", rootIdentity: root, persisted: []model.ProcessIdentity{root}, owned: map[int32]model.ProcessIdentity{}, groupProbe: func(pid int, sig syscall.Signal) error {
				if pid != -int(root.PID) || sig != 0 {
					t.Fatalf("unsafe group operation: pid=%d sig=%d", pid, sig)
				}
				return groupErr
			}, signal: func(int, syscall.Signal) error { t.Fatal("unknown group member must not be signaled"); return nil }}
			result := h.Stop(context.Background(), model.StopPolicy{Timeout: time.Second})
			if result.Complete || len(result.Remaining) != 0 || !strings.Contains(strings.Join(result.Errors, " "), "recovery_required") {
				t.Fatalf("group uncertainty claimed clean: %+v", result)
			}
			journal.mu.Lock()
			defer journal.mu.Unlock()
			if len(journal.finishes) != 1 || journal.finishes[0] || len(journal.identities) != 1 || journal.identities[0] != root {
				t.Fatalf("intent purged instead of requiring recovery: finishes=%v identities=%v", journal.finishes, journal.identities)
			}
		})
	}
}
func TestManagedAbsentLaunchGroupAllowsCleanFinish(t *testing.T) {
	root := model.ProcessIdentity{PID: 12345, CreatedMillis: 1}
	journal := &memoryJournal{identities: []model.ProcessIdentity{root}}
	h := &handle{reader: &changingTopologyReader{infos: map[int32]process.Info{}}, journal: journal, launchID: "test", rootIdentity: root, persisted: []model.ProcessIdentity{root}, owned: map[int32]model.ProcessIdentity{}, groupProbe: func(pid int, sig syscall.Signal) error {
		if sig != 0 {
			t.Fatal("group signal")
		}
		return syscall.ESRCH
	}}
	result := h.Stop(context.Background(), model.StopPolicy{})
	if !result.Complete {
		t.Fatalf("empty group blocked clean finish: %+v", result)
	}
	if len(journal.finishes) != 1 || !journal.finishes[0] || len(journal.identities) != 0 {
		t.Fatalf("clean group retained intent: %+v", journal)
	}
}

func TestManagedFastParentOrphanPreservesRecoveryJournal(t *testing.T) {
	reader := process.NewReader()
	journal := &memoryJournal{}
	h, err := NewManaged(process.NewSharedReader(reader, func() time.Duration { return time.Second }), journal).Start(context.Background(), model.Service{Command: []string{"/bin/sh", "-c", "sleep 60 & exit 0"}, Stop: model.StopPolicy{Timeout: time.Second}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := h.(*handle).rootIdentity
	var child model.ProcessIdentity
	// Test-only cleanup identifies this fixture's group member freshly, then
	// verifies its exact birth again before sending an individual PID signal.
	t.Cleanup(func() {
		all, _ := reader.List(context.Background())
		for _, candidate := range all {
			if candidate.PGID != root.PID || candidate.Identity.PID == root.PID {
				continue
			}
			info, err := process.ReadIdentity(context.Background(), reader, candidate.Identity.PID)
			if err == nil && info.PGID == root.PID && info.Identity.CreatedMillis >= root.CreatedMillis {
				verifiedSignal(context.Background(), reader, info.Identity, func(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) })
			}
		}
	})
	select {
	case result := <-h.Done():
		if result.Err == nil || !strings.Contains(result.Err.Error(), "recovery_required") {
			t.Fatalf("fast parent's unseen child was reported clean: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fast parent did not complete bounded observation")
	}
	all, err := reader.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range all {
		if candidate.PGID == root.PID && candidate.Identity.PID != root.PID {
			info, err := process.ReadIdentity(context.Background(), reader, candidate.Identity.PID)
			if err == nil && process.Live(info) {
				child = info.Identity
				break
			}
		}
	}
	if child.PID == 0 {
		t.Fatal("fixture did not retain its unobserved child")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.finishes) == 0 || journal.finishes[len(journal.finishes)-1] || len(journal.identities) != 1 || journal.identities[0] != root {
		t.Fatalf("unsafe journal state: finishes=%v identities=%v child=%+v", journal.finishes, journal.identities, child)
	}
	for _, complete := range journal.finishes {
		if complete {
			t.Fatal("orphaned launch journal was marked complete")
		}
	}
}
func TestManagedShortSuccessfulCommandHasNoFalseRecovery(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	journal := &memoryJournal{}
	h, err := NewManaged(process.NewSharedReader(process.NewReader(), func() time.Duration { return time.Second }), journal).Start(context.Background(), model.Service{Command: []string{truePath}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-h.Done():
		if result.Err != nil || result.Code != 0 {
			t.Fatalf("short command falsely required recovery: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("short command stalled")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if len(journal.finishes) != 1 || !journal.finishes[0] || len(journal.identities) != 0 {
		t.Fatalf("short command retained recovery: finishes=%v identities=%v", journal.finishes, journal.identities)
	}
}

func TestManagedStopBypassesSleepingTopologyWait(t *testing.T) {
	reader := process.NewSharedReader(process.NewReader(), func() time.Duration { return time.Hour })
	h, err := NewManaged(reader, &memoryJournal{}).Start(context.Background(), model.Service{Command: []string{"/bin/sleep", "60"}, Stop: model.StopPolicy{Timeout: time.Second}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.Stop(ctx, model.StopPolicy{Timeout: time.Second})
	})
	local := h.(*handle)
	deadline := time.Now().Add(2 * time.Second)
	for {
		local.captureMu.Lock()
		observed := local.lastTopology > 0
		local.captureMu.Unlock()
		if observed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("managed watcher did not observe initial generation")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if result := h.Stop(ctx, model.StopPolicy{Timeout: time.Second}); !result.Complete {
		t.Fatalf("stop waited on cached topology expiry: %+v", result)
	}
	select {
	case <-h.Done():
	case <-ctx.Done():
		t.Fatal("finished process retained sleeping watcher")
	}
}
