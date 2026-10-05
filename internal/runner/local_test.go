package runner

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestRunnerFixture(t *testing.T) {
	mode := os.Getenv("SH_FIXTURE")
	if mode == "" {
		return
	}
	switch mode {
	case "echo":
		d, _ := os.Getwd()
		fmt.Println(d, os.Getenv("SH_VALUE"))
		for _, a := range os.Args {
			if strings.Contains(a, "$(") {
				fmt.Println(a)
			}
		}
	case "parent", "root-exit", "late-parent":
		c := exec.Command(os.Args[0], "-test.run=TestRunnerFixture")
		c.Env = append(os.Environ(), "SH_FIXTURE=child")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := c.Start(); err != nil {
			os.Exit(3)
		}
		fmt.Printf("child=%d\n", c.Process.Pid)
		if mode == "root-exit" {
			time.Sleep(700 * time.Millisecond)
			os.Exit(0)
		}
		if mode == "late-parent" {
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, syscall.SIGTERM)
			<-ch
			c2 := exec.Command(os.Args[0], "-test.run=TestRunnerFixture")
			c2.Env = append(os.Environ(), "SH_FIXTURE=child")
			c2.Stdout = os.Stdout
			c2.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			c2.Start()
			time.Sleep(time.Second)
			os.Exit(0)
		}
		select {}
	case "child":
		signal.Ignore(syscall.SIGTERM)
		fmt.Printf("ready=%d\n", os.Getpid())
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(0)
}
func start(t *testing.T, mode string) (Handle, chan string) {
	t.Helper()
	lines := make(chan string, 100)
	r := NewLocal(process.NewReader())
	h, e := r.Start(context.Background(), model.Service{Command: []string{os.Args[0], "-test.run=TestRunnerFixture", "--", "$(touch unexpected)"}, Cwd: t.TempDir(), Env: map[string]string{"SH_FIXTURE": mode, "SH_VALUE": "教材"}, Stop: model.StopPolicy{Signal: "TERM", Timeout: time.Second}}, func(stream, line string) {
		select {
		case lines <- line:
		default:
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 4*time.Second)
		defer c()
		h.Stop(ctx, model.StopPolicy{Signal: "TERM", Timeout: time.Second})
	})
	return h, lines
}
func TestLocalArgvCwdAndEnv(t *testing.T) {
	h, lines := start(t, "echo")
	select {
	case res := <-h.Done():
		if res.Err != nil || res.Code != 0 {
			t.Fatal(res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exit timeout")
	}
	all := ""
	for len(lines) > 0 {
		all += <-lines
	}
	if !strings.Contains(all, "教材") || !strings.Contains(all, "$(touch unexpected)") {
		t.Fatal(all)
	}
}
func TestStopNewSessionAndLateChild(t *testing.T) {
	for _, mode := range []string{"parent", "late-parent"} {
		t.Run(mode, func(t *testing.T) {
			h, _ := start(t, mode)
			deadline := time.Now().Add(5 * time.Second)
			for len(h.Identities()) < 2 && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if len(h.Identities()) < 2 {
				t.Fatal("child not tracked")
			}
			result := h.Stop(context.Background(), model.StopPolicy{Signal: "TERM", Timeout: time.Second})
			if !result.Complete || len(result.Remaining) > 0 {
				t.Fatal(result)
			}
		})
	}
}
func TestRootExitCleansRecordedChildren(t *testing.T) {
	h, _ := start(t, "root-exit")
	select {
	case result := <-h.Done():
		if result.Err != nil {
			t.Fatal(result)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("root exit cleanup timed out")
	}
	if len(h.Identities()) != 0 {
		t.Fatal("child remains")
	}
}

type identityReader struct {
	info process.Info
	err  error
}

func (r identityReader) List(context.Context) ([]process.Info, error) {
	return []process.Info{r.info}, r.err
}
func (r identityReader) Read(context.Context, int32) (process.Info, error) { return r.info, r.err }
func TestStopRejectsReusedPIDAndPermissionDenied(t *testing.T) {
	id := model.ProcessIdentity{PID: 12345, CreatedMillis: 100}
	for _, r := range []identityReader{{info: process.Info{Identity: model.ProcessIdentity{PID: 12345, CreatedMillis: 200}}}, {err: syscall.EPERM}} {
		signals := 0
		err := verifiedSignal(context.Background(), r, id, func(int) error { signals++; return nil })
		if err == nil || signals != 0 {
			t.Fatal("unsafe signal", err, signals)
		}
	}
	err := verifiedSignal(context.Background(), identityReader{info: process.Info{Identity: id}}, id, func(int) error { return syscall.EPERM })
	if err == nil {
		t.Fatal("permission denied must propagate")
	}
}

func TestConcurrentStopHonorsDeadline(t *testing.T) {
	h, lines := start(t, "child")
	select {
	case <-lines:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture not ready")
	}
	local := h.(*handle)
	signalled := make(chan struct{})
	var once sync.Once
	local.signal = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGTERM {
			once.Do(func() { close(signalled) })
		}
		return syscall.Kill(pid, sig)
	}
	first := make(chan StopResult, 1)
	go func() { first <- h.Stop(context.Background(), model.StopPolicy{Timeout: time.Second}) }()
	<-signalled
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	before := time.Now()
	result := h.Stop(ctx, model.StopPolicy{Timeout: time.Second})
	if elapsed := time.Since(before); elapsed > 250*time.Millisecond {
		t.Errorf("concurrent stop exceeded deadline: %v", elapsed)
	}
	if result.Complete {
		t.Error("pending cleanup misreported complete")
	}
	select {
	case <-first:
	case <-time.After(3 * time.Second):
		t.Fatal("first cleanup blocked")
	}
}
