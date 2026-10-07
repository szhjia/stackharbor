package supervisor

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/runner"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRedactSensitiveEnvironmentValues(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
	}{
		{"short password", "DB_PASSWORD", "abc"},
		{"API key", "SERVICE_API_KEY", "key-1234"},
		{"short pass alias", "DB_PASS", "pw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(model.Service{Env: map[string]string{tc.key: tc.value}}, "credential="+tc.value)
			if strings.Contains(got, tc.value) {
				t.Fatalf("sensitive environment value leaked: %q", got)
			}
		})
	}
}

func TestRedactOverlappingSecrets(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		line string
	}{
		{"prefix", map[string]string{"SHORT_TOKEN": "secret", "LONG_TOKEN": "secret-suffix"}, "credential=secret-suffix"},
		{"offset", map[string]string{"FIRST_TOKEN": "abc", "SECOND_TOKEN": "bcd"}, "credential=abcd"},
		{"marker", map[string]string{"SHORT_TOKEN": "red", "LONG_TOKEN": "secret"}, "value=secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 100 {
				want := "credential=[redacted]"
				if tc.name == "marker" {
					want = "value=[redacted]"
				}
				if got := Redact(model.Service{Env: tc.env}, tc.line); got != want {
					t.Fatalf("partial secret leaked or marker corrupted: %q", got)
				}
			}
		})
	}
}

func TestRedactURLPasswordOutsideURL(t *testing.T) {
	spec := model.Service{Env: map[string]string{"DATABASE_URL": "postgres://alice:pw123@localhost/db"}}
	if got := Redact(spec, "password=pw123"); got != "password=[redacted]" {
		t.Fatalf("URL password leaked outside URL: %q", got)
	}
}

type phaseRunner struct {
	mu                    sync.Mutex
	calls                 map[string]int
	checkCode, verifyCode int
}

func (r *phaseRunner) Start(ctx context.Context, n model.Service, out func(string, string)) (runner.Handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := n.Command[0]
	r.calls[key]++
	h := &fakeHandle{done: make(chan runner.ExitResult, 1), complete: true, stops: &[]string{}, id: string(n.ID)}
	if key == "api" {
		return h, nil
	}
	code := 0
	status := "satisfied"
	if key == "check" {
		code = r.checkCode
		if code == 10 {
			status = "needed"
		}
		if code == 20 {
			status = "drift"
		}
	}
	if key == "verify" {
		code = r.verifyCode
	}
	out("stdout", `{"schema_version":1,"status":"`+status+`","checked_scope":["chain"]}`)
	h.done <- runner.ExitResult{Code: code}
	return h, nil
}
func TestWhenNeededRechecksAndRequiresVerification(t *testing.T) {
	for _, tc := range []struct {
		check, verify int
		success       bool
	}{{0, 0, true}, {10, 0, true}, {10, 7, false}, {20, 0, false}} {
		t.Run(fmt.Sprint(tc.check, "/", tc.verify), func(t *testing.T) {
			t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
			task := service("app/task/migrate")
			task.Kind = "task"
			task.Command = []string{"run"}
			task.Task = &model.TaskSpec{Policy: "when-needed", Effect: "read-only", TimeoutSeconds: 2, Check: &model.TaskCommand{Command: []string{"check"}}, Verify: &model.TaskCommand{Command: []string{"verify"}}}
			api := service("app/api", task.ID)
			api.Command = []string{"api"}
			r := &phaseRunner{calls: map[string]int{}, checkCode: tc.check, verifyCode: tc.verify}
			s, e := New(model.Workspace{Root: t.TempDir(), Projects: []model.Project{{ID: "app", Services: []model.Service{task, api}}}}, r, observe.NewPortProbe(), nil)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Shutdown(context.Background())
			e = s.Start(context.Background(), []model.ServiceID{api.ID})
			if (e == nil) != tc.success {
				t.Fatal(e)
			}
			r.mu.Lock()
			runs, apis := r.calls["run"], r.calls["api"]
			r.mu.Unlock()
			if tc.check == 0 || tc.check == 20 {
				if runs != 0 {
					t.Fatal("unnecessary migration")
				}
			}
			if !tc.success && apis != 0 {
				t.Fatal("released before verify")
			}
			if tc.check == 0 {
				if e = s.Restart(context.Background(), []model.ServiceID{api.ID}); e != nil {
					t.Fatal(e)
				}
				r.mu.Lock()
				checks, runs := r.calls["check"], r.calls["run"]
				r.mu.Unlock()
				if checks != 2 || runs != 0 {
					t.Fatal("restart failed to recheck", checks, runs)
				}
			}
		})
	}
}

func TestCheckExitContractIsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		code  int
		text  string
		valid bool
	}{{0, `{"schema_version":1,"status":"satisfied","checked_scope":["chain"]}`, true}, {10, `{"schema_version":1,"status":"needed","checked_scope":["chain"]}`, true}, {20, `{"schema_version":1,"status":"drift","checked_scope":["chain"]}`, true}, {0, `{"schema_version":1,"status":"needed","checked_scope":["chain"]}`, false}, {1, `{"schema_version":1,"status":"satisfied","checked_scope":["chain"]}`, false}, {0, `{}`, false}} {
		_, e := parseStatus(tc.code, tc.text)
		if (e == nil) != tc.valid {
			t.Fatal(tc, e)
		}
	}
}

func TestTaskCompletionGateAndBlockedFailure(t *testing.T) {
	task := service("app/task/check")
	task.Kind = "task"
	task.Task = &model.TaskSpec{Effect: "read-only", Policy: "always", TimeoutSeconds: 2}
	api := service("app/api", task.ID)
	s, f := setup(t, task, api)
	done := make(chan error, 1)
	go func() { done <- s.Start(context.Background(), []model.ServiceID{api.ID}) }()
	var h *fakeHandle
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		h = f.handles[task.ID]
		f.mu.Unlock()
		if h != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if h == nil {
		t.Fatal("task never started")
	}
	time.Sleep(30 * time.Millisecond)
	f.mu.Lock()
	started := f.starts[api.ID]
	f.mu.Unlock()
	if started != 0 {
		t.Fatal("API released before task completion")
	}
	h.done <- runner.ExitResult{Code: 7}
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("failure hidden")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked forever")
	}
	for _, v := range s.Snapshot().Services {
		if v.Spec.ID == api.ID && v.State != "blocked" {
			t.Fatal("unspawned child not blocked", v.State)
		}
	}
}
