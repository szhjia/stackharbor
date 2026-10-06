package supervisor

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerDependenciesAndAllActions(t *testing.T) {
	root := t.TempDir()
	calls := filepath.Join(root, "docker-calls")
	bin := filepath.Join(root, "bin")
	os.Mkdir(bin, 0700)
	os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\ncase \"$1\" in info) printf '\"fixture-daemon\"'; exit;; esac\ncase \"$*\" in *' config '*) printf '{\"name\":\"fixture-project\"}';exit;; esac\nprintf '%s\\n' \"$*\" >> \"$SH_DOCKER_CALLS\"\ncase \"$*\" in *' ps '*) printf '[]';; esac\n"), 0700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SH_DOCKER_CALLS", calls)
	t.Setenv("STACKHARBOR_CACHE_DIR", t.TempDir())
	os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  db:\n    image: postgres\n  redis:\n    image: redis\n  migrate:\n    image: app\n"), 0600)
	native := service("app/api")
	native.DockerDependsOn = []string{"db", "redis"}
	f := &fakeRunner{starts: map[model.ServiceID]int{}, handles: map[model.ServiceID]*fakeHandle{}}
	s, err := New(model.Workspace{Root: root, Projects: []model.Project{{ID: "app", Services: []model.Service{native}}}}, f, observe.NewPortProbe(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	ctx := context.Background()
	if err := s.AllAction(ctx, "start"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], " db") || !strings.HasSuffix(lines[1], " redis") || f.starts[native.ID] != 1 {
		t.Fatal(string(raw), f.starts)
	}
	if err := s.DockerAction(ctx, "restart", []string{"db"}); err != nil {
		t.Fatal(err)
	}
	if f.starts[native.ID] != 2 {
		t.Fatal("native dependent not restored", f.starts)
	}
	if err := s.AllAction(ctx, "stop"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(calls)
	if strings.Contains(string(raw), "migrate") {
		t.Fatal("unrelated migration ran")
	}
	redisStop := strings.LastIndex(string(raw), "stop --timeout 15 redis")
	dbStop := strings.LastIndex(string(raw), "stop --timeout 15 db")
	if redisStop < 0 || dbStop < redisStop || s.Snapshot().Services[0].State != "stopped" {
		t.Fatal("stop order wrong", string(raw))
	}
}
