package sessionhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestCompletionRecordSurvivesSocketClosure(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	h, err := New(context.Background(), model.Workspace{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	info := h.SessionInfo()
	client, err := sessionapi.Connect(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(info.SocketPath); !os.IsNotExist(err) {
		t.Fatalf("socket remained: %v", err)
	}
	record, err := ReadCompletion(info.CacheDir, info.SessionID)
	if err != nil || record.State != "succeeded" || record.SessionID != info.SessionID {
		t.Fatalf("record=%+v error=%v", record, err)
	}
	if _, err := ReadCompletion(info.CacheDir, "00000000000000000000000000000000"); err == nil {
		t.Fatal("missing record claimed success")
	}
	if h.Result() != nil {
		t.Fatal(h.Result())
	}
}

func TestFinalizationFailureCannotLeaveSuccess(t *testing.T) {
	for _, kind := range []string{"journal", "terminal-journal", "unlock"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
			h, err := New(context.Background(), model.Workspace{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			info := h.SessionInfo()
			if kind == "journal" {
				h.writeRecord = func(context.Context, string, control.Operation) error { return errors.New("journal unavailable") }
			} else if kind == "terminal-journal" {
				h.writeRecord = func(ctx context.Context, cache string, op control.Operation) error {
					if op.State != "finalizing" {
						return errors.New("terminal journal unavailable")
					}
					return writeCompletion(ctx, cache, op)
				}
			} else {
				h.finalizeBackend = func() error { return errors.Join(h.backend.Finalize(), errors.New("unlock failed")) }
			}
			if err = h.Close(context.Background()); err == nil {
				t.Fatal("finalization claimed success")
			}
			if h.Result() == nil {
				t.Fatal("host result lost error")
			}
			op, err := ReadCompletion(info.CacheDir, info.SessionID)
			if err == nil && op.State == "succeeded" {
				t.Fatal("false journal success")
			}
		})
	}
}
func TestCompletionUnknownPrivateAndBounded(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	cache := filepath.Join(root, "cache")
	if err := os.Mkdir(cache, 0700); err != nil {
		t.Fatal(err)
	}
	id := "11111111111111111111111111111111"
	pending := control.Operation{SessionID: id, Action: "close", State: "finalizing", UpdatedAt: time.Now().UTC()}
	if err := writeCompletion(context.Background(), cache, pending); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCompletion(cache, id); err == nil {
		t.Fatal("interrupted finalization claimed terminal")
	}
	path := filepath.Join(cache, "completions", id+".json")
	st, _ := os.Stat(path)
	dir, _ := os.Stat(filepath.Dir(path))
	if st.Mode().Perm() != 0600 || dir.Mode().Perm() != 0700 {
		t.Fatal("completion not private")
	}
	// Seed ordinary files then exercise one pruning pass; no repeated fsyncs.
	for i := 0; i < 1001; i++ {
		op := control.Operation{SessionID: fmt.Sprintf("%032x", i+2), Action: "close", State: "succeeded", UpdatedAt: time.Now().Add(-time.Minute)}
		data, _ := json.Marshal(op)
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), op.SessionID+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	expired := control.Operation{SessionID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Action: "close", State: "succeeded", UpdatedAt: time.Now().Add(-25 * time.Hour)}
	data, _ := json.Marshal(expired)
	os.WriteFile(filepath.Join(filepath.Dir(path), expired.SessionID+".json"), data, 0600)
	if err := writeCompletion(context.Background(), cache, pending); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	records := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".json" {
			records++
		}
	}
	if records > 1000 {
		t.Fatal("journal unbounded", len(entries))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), expired.SessionID+".json")); !os.IsNotExist(err) {
		t.Fatal("expired record retained")
	}
}

func TestHostProcessFixture(t *testing.T) {
	if os.Getenv("SH_HOST_FIXTURE") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("SH_HOST_PID_FILE"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		panic(err)
	}
	for {
		time.Sleep(time.Second)
	}
}
func TestCloseCancelsQueuedRestart(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	root := t.TempDir()
	pidfile := filepath.Join(root, "pid")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	node := model.Service{ID: "app/web", ProjectID: "app", Cwd: root, Command: []string{os.Args[0], "-test.run=^TestHostProcessFixture$"}, Env: map[string]string{"SH_HOST_FIXTURE": "1", "SH_HOST_PID_FILE": pidfile}, Ready: &model.ReadyProbe{TCP: address, TimeoutSeconds: 20}, Stop: model.StopPolicy{TimeoutSeconds: 1}}
	h, err := New(context.Background(), model.Workspace{Root: root, Projects: []model.Project{{ID: "app", Services: []model.Service{node}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	start, err := h.coordinator.Plan(context.Background(), control.PlanRequest{Action: "start", Targets: []string{"app/web"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.coordinator.Submit(context.Background(), control.SubmitRequest{PlanID: start.ID, IdempotencyKey: control.NewIdempotencyKey(start.ID)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(pidfile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture launch absent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	restart, err := h.coordinator.Plan(context.Background(), control.PlanRequest{Action: "restart", Targets: []string{"app/web"}})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := h.coordinator.Submit(context.Background(), control.SubmitRequest{PlanID: restart.ID, IdempotencyKey: control.NewIdempotencyKey(restart.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if queued.State != "queued" {
		t.Fatal(queued)
	}
	if err = h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := h.coordinator.Operation(queued.ID)
	if err != nil || op.State != "canceled" {
		t.Fatalf("queued restart %+v error %v", op, err)
	}
	data, _ := os.ReadFile(pidfile)
	pid, _ := strconv.Atoi(string(data))
	if err = syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("fixture survived %d %v", pid, err)
	}
}

func TestJournalLockHonorsCloseBudget(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	cache := filepath.Join(root, "cache")
	dir := filepath.Join(cache, "completions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := lockJournal(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	op := control.Operation{SessionID: "11111111111111111111111111111111", Action: "close", State: "succeeded", UpdatedAt: time.Now()}
	if err = writeCompletion(ctx, cache, op); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("journal ignored budget", err)
	}
	if _, err = ReadCompletion(cache, op.SessionID); err == nil {
		t.Fatal("blocked journal recorded success")
	}
}
