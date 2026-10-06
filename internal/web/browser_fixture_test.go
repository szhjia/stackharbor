package web

import (
	"context"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/logs"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in fixture owns all roots, child processes and private discovery state.
// It serves the actual embedded UI with the production gateway/session protocol.
func TestBrowserFixture(t *testing.T) {
	dir := os.Getenv("STACKHARBOR_BROWSER_FIXTURE")
	if dir == "" {
		t.Skip("manual browser verification fixture")
	}
	ns := namespace(t)
	t.Setenv("STACKHARBOR_CACHE_DIR", ns)
	root := filepath.Join(t.TempDir(), "harbor-cafe")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	host, err := sessionhost.New(context.Background(), model.Workspace{Root: root, Projects: []model.Project{{ID: "cafe", Services: []model.Service{{ID: "cafe/api", Name: "Cafe API", Kind: "service", Cwd: root, Command: []string{"/bin/sh", "-c", "printf 'fixture ready\n<img src=x onerror=alert(1)>\n'; sleep 240"}}, {ID: "cafe/build", Name: "Build assets", Kind: "task", Cwd: root, Command: []string{"/bin/sh", "-c", "printf 'build fixture complete\n'"}, Task: &model.TaskSpec{}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	if err = host.Controller().Start(context.Background(), []model.ServiceID{"cafe/api"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2005; i++ {
		host.Controller().Logs().Append(logs.Entry{ServiceID: "cafe/api", ProjectID: "cafe", Text: "fixture retained output"})
	}
	host.Controller().Logs().Append(logs.Entry{ServiceID: "cafe/api", ProjectID: "cafe", Text: "<img src=x onerror=alert(1)>"})
	secondRoot := filepath.Join(t.TempDir(), "worker-dock")
	if err = os.Mkdir(secondRoot, 0700); err != nil {
		t.Fatal(err)
	}
	second, err := sessionhost.New(context.Background(), model.Workspace{Root: secondRoot, Projects: []model.Project{{ID: "dock", Services: []model.Service{{ID: "dock/worker", Name: "Dock worker", Cwd: secondRoot, Command: []string{"false"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	collector := inventory.New(registrySource{ns}, inventory.Options{})
	g := NewGateway(address, Options{Namespace: ns, DevelopmentOrigin: os.Getenv("STACKHARBOR_BROWSER_DEV_ORIGIN"), Collect: func(ctx context.Context) (inventory.Inventory, error) {
		v, e := collector.Collect(ctx)
		if _, err := os.Stat(filepath.Join(dir, "stale")); err == nil {
			for i := range v.Sessions {
				v.Sessions[i].Stale = true
				if v.Sessions[i].Snapshot != nil {
					v.Sessions[i].Snapshot.ObservedAt = time.Now().Add(-10 * time.Second)
				}
			}
		}
		return v, e
	}})
	g.Start(ctx)
	server := &http.Server{Handler: g, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer func() { server.Close() }()
	if err = os.WriteFile(filepath.Join(dir, "backend-url"), []byte("http://"+address), 0600); err != nil {
		t.Fatal(err)
	}
	credential := func() {
		token, e := g.auth.issue()
		if e != nil {
			t.Fatal(e)
		}
		origin := "http://" + address
		if dev := os.Getenv("STACKHARBOR_BROWSER_DEV_ORIGIN"); dev != "" {
			origin = dev
		}
		if e = os.WriteFile(filepath.Join(dir, "url"), []byte(origin+"/#token="+token), 0600); e != nil {
			t.Fatal(e)
		}
	}
	credential()
	nextCredential := time.Now().Add(15 * time.Second)
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(dir, "stop")); err == nil {
			break
		}
		if _, err = os.Stat(filepath.Join(dir, "disconnect")); err == nil {
			os.Remove(filepath.Join(dir, "disconnect"))
			server.Close()
			time.Sleep(1500 * time.Millisecond)
			listener, err = net.Listen("tcp4", address)
			if err != nil {
				t.Fatal(err)
			}
			server = &http.Server{Handler: g, ReadHeaderTimeout: 5 * time.Second}
			go server.Serve(listener)
		}
		if time.Now().After(nextCredential) {
			credential()
			nextCredential = time.Now().Add(15 * time.Second)
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
}
