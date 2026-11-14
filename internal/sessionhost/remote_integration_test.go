package sessionhost

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/tui"
)

func TestRemoteProcessFixture(t *testing.T) {
	if os.Getenv("SH_REMOTE_FIXTURE") != "1" {
		return
	}
	fmt.Println("retained host log")
	if err := os.WriteFile(os.Getenv("SH_REMOTE_PID_FILE"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestRemoteTUIQuitAndControlCDetachWithoutStoppingHost(t *testing.T) {
	for _, key := range []string{"q", "\x03"} {
		t.Run(fmt.Sprintf("key-%x", key), func(t *testing.T) {
			t.Setenv("STACKHARBOR_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
			t.Setenv("TERM", "xterm-256color")
			root := t.TempDir()
			pidfile := filepath.Join(root, "pid")
			spec := model.Service{ID: "app/api", ProjectID: "app", Key: "api", Cwd: root, Command: []string{os.Args[0], "-test.run=^TestRemoteProcessFixture$"}, Env: map[string]string{"SH_REMOTE_FIXTURE": "1", "SH_REMOTE_PID_FILE": pidfile}, Stop: model.StopPolicy{TimeoutSeconds: 1}}
			w := model.Workspace{Root: root, Projects: []model.Project{{ID: "app", Services: []model.Service{spec}}}}
			h, err := New(context.Background(), w)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close(context.Background())
			r, err := NewRemote(context.Background(), w, h.SessionInfo())
			if err != nil {
				t.Fatal(err)
			}
			defer r.Shutdown(context.Background())
			if err = r.Start(context.Background(), []model.ServiceID{spec.ID}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			pid := 0
			for {
				data, e := os.ReadFile(pidfile)
				if e == nil {
					pid, _ = strconv.Atoi(string(data))
					if pid > 0 {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatal("service never launched")
				}
				time.Sleep(10 * time.Millisecond)
			}
			for {
				if err = r.refresh(context.Background()); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, entry := range r.Logs().Entries(nil) {
					found = found || strings.Contains(entry.Text, "retained host log")
				}
				if found {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("retained logs missing")
				}
				time.Sleep(10 * time.Millisecond)
			}
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer slave.Close()
			if err = pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 100}); err != nil {
				t.Fatal(err)
			}
			ready := make(chan struct{})
			go func() {
				buffer := make([]byte, 1024)
				if n, _ := master.Read(buffer); n > 0 {
					close(ready)
				}
				_, _ = io.Copy(io.Discard, master)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- tui.Run(ctx, r, slave, slave) }()
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("TUI never rendered")
			}
			if _, err = master.Write([]byte(key)); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("TUI did not detach")
			}
			if err = syscall.Kill(pid, 0); err != nil {
				t.Fatal("TUI exit stopped service", err)
			}
			select {
			case <-h.Done():
				t.Fatal("TUI exit closed host")
			default:
			}
			client, err := sessionapi.Connect(context.Background(), h.SessionInfo())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			snapshot, err := client.Snapshot(context.Background())
			if err != nil || len(snapshot.Processes) != 1 || snapshot.Processes[0].PID != int32(pid) {
				t.Fatal("session changed after detach", snapshot, err)
			}
			reconnected, err := NewRemote(context.Background(), w, h.SessionInfo())
			if err != nil {
				t.Fatal(err)
			}
			defer reconnected.Shutdown(context.Background())
			if len(reconnected.Logs().Entries(nil)) == 0 {
				t.Fatal("reconnected client lost retained logs")
			}
		})
	}
}
