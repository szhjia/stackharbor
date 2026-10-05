package tests

import (
	"context"
	"github.com/szhjia/stackharbor/internal/discovery"
	"github.com/szhjia/stackharbor/internal/graph"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"github.com/szhjia/stackharbor/internal/process"
	"github.com/szhjia/stackharbor/internal/runner"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExamplesValidateAndHavePortablePaths(t *testing.T) {
	for _, name := range []string{"minimal", "multi-service", "harbor-cafe"} {
		w := discovery.Discover(context.Background(), discovery.Options{Root: filepath.Join("..", "examples", name)})
		g, ds := graph.Build(w.Services())
		if w.Invalid() || g == nil || len(ds) > 0 || len(w.Projects) == 0 {
			t.Fatal(name, w.Diagnostics, ds)
		}
	}
	p := filepath.Join("..", "examples", "pagesmith", "stackharbor.workspace.yaml")
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "/Users/") || !strings.Contains(string(b), "../../..") {
		t.Fatal("nonportable example")
	}
	if _, e := os.Stat(filepath.Join("..", "..", "backend")); e == nil {
		w := discovery.Discover(context.Background(), discovery.Options{WorkspaceFile: p})
		if w.Invalid() || len(w.Projects) != 4 {
			t.Fatal("PageSmith must register four projects", w.Diagnostics, w.Projects)
		}
	}
}
func freePort(t *testing.T) int {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func TestTwoMonoreposLifecycleAndExternalPort(t *testing.T) {
	for _, name := range []string{"minimal", "multi-service"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
			w := discovery.Discover(context.Background(), discovery.Options{Root: filepath.Join("..", "examples", name)})
			if w.Invalid() || len(w.Projects) == 0 {
				t.Fatal(w.Diagnostics)
			}
			for pi := range w.Projects {
				for si := range w.Projects[pi].Services {
					v := &w.Projects[pi].Services[si]
					port := freePort(t)
					extra := freePort(t)
					for extra == port {
						extra = freePort(t)
					}
					v.Command = []string{os.Args[0]}
					v.Env = map[string]string{"SH_HTTP_FIXTURE": "1", "SH_HTTP_PORT": strconv.Itoa(port), "SH_HTTP_EXTRA": strconv.Itoa(extra)}
					v.Ports = []model.Port{{Name: "http", Number: port}}
					v.Ready = &model.ReadyProbe{HTTP: "http://127.0.0.1:" + strconv.Itoa(port), Timeout: 5 * time.Second}
				}
			}
			reader := process.NewReader()
			s, e := supervisor.New(w, runner.NewLocal(reader), observe.NewPortProbe(), observe.NewSampler(reader))
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
			ids := []model.ServiceID{}
			for _, v := range w.Services() {
				ids = append(ids, v.ID)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if e = s.Start(ctx, ids); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				if t.Failed() {
					for _, v := range s.Snapshot().Services {
						t.Logf("service=%s state=%s owned=%v metric=%+v ports=%+v", v.Spec.ID, v.State, v.Owned, v.Metric, v.Ports)
					}
				}
			})
			wait(t, func() bool {
				snap := s.Snapshot()
				for _, v := range snap.Services {
					if v.State != "running" || !v.Metric.Known || len(v.Ports) != 2 {
						return false
					}
				}
				for _, v := range snap.Services {
					for _, port := range v.Ports {
						if port.Status != "owned" {
							return false
						}
					}
				}
				return len(s.Logs().Entries(nil)) > 0
			})
			owned := []model.ProcessIdentity{}
			for _, v := range s.Snapshot().Services {
				owned = append(owned, v.Owned...)
			}
			if e = s.Shutdown(ctx); e != nil {
				t.Fatal(e)
			}
			for _, id := range owned {
				if p, e := reader.Read(ctx, id.PID); e == nil && p.Identity == id && process.Live(p) {
					t.Fatal("owned fixture survived")
				}
			}
		})
	}
	external, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer external.Close()
	port := external.Addr().(*net.TCPAddr).Port
	if e = observe.NewPortProbe().CheckStart(context.Background(), []model.Port{{Name: "http", Number: port}}); e == nil {
		t.Fatal("external port accepted")
	}
	conn, e := net.DialTimeout("tcp", external.Addr().String(), time.Second)
	if e != nil {
		t.Fatal("external listener stopped")
	}
	conn.Close()
}

func TestSkillDiscoveryRegistrationValidation(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"skill-demo","packageManager":"pnpm@11.9.0","scripts":{"dev":"vite"}}`), 0600)
	for _, args := range [][]string{{"discover", "--root", root, "--json"}, {"init", "--root", root, "--dry-run"}, {"init", "--root", root, "--write"}, {"validate", "--root", root, "--json"}} {
		cmd := exec.Command(binary, args...)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatal(args, e, string(out))
		}
	}
	b, e := os.ReadFile(filepath.Join(root, "stackharbor.yaml"))
	if e != nil || !strings.Contains(string(b), "pnpm") {
		t.Fatal("skill workflow failed", e, string(b))
	}
}

func TestExternalProjectMetricsDoNotTakeOwnership(t *testing.T) {
	port := freePort(t)
	external := exec.Command(os.Args[0])
	external.Env = append(os.Environ(), "SH_HTTP_FIXTURE=1", "SH_HTTP_PORT="+strconv.Itoa(port))
	if err := external.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { external.Process.Kill(); external.Wait() }()
	wait(t, func() bool {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
		}
		return err == nil
	})
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	reader := process.NewReader()
	spec := model.Service{ID: "external/dev", ProjectID: "external", Ports: []model.Port{{Number: port}}}
	s, err := supervisor.New(model.Workspace{Root: t.TempDir(), Projects: []model.Project{{ID: "external", Services: []model.Service{spec}}}}, runner.NewLocal(reader), observe.NewPortProbe(), observe.NewSampler(reader))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	wait(t, func() bool {
		v := s.Snapshot().Services[0]
		return v.Metric.Known && v.Metric.RSS > 0 && v.Metric.CPUPercent != nil
	})
	v := s.Snapshot().Services[0]
	if v.MetricSource != "external" || v.State != "stopped" || len(v.Owned) != 0 {
		t.Fatal("observation changed ownership", v)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p, err := reader.Read(context.Background(), int32(external.Process.Pid)); err != nil || !process.Live(p) {
		t.Fatal("external listener stopped", err)
	}
}
