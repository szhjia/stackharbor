//go:build darwin

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
)

func TestManagedDarwinStartStopNeverSpawnsPS(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ps-was-invoked")
	script := "#!/bin/sh\nprintf called > \"$STACKHARBOR_PS_MARKER\"\nexec /bin/ps \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "ps"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("STACKHARBOR_PS_MARKER", marker)
	reader := process.NewSharedReader(process.NewReader(), func() time.Duration { return 5 * time.Second })
	// Prime the shared snapshot before launch. Start and Stop must bypass it.
	if _, err := reader.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	handle, err := NewManaged(reader, &memoryJournal{}).Start(context.Background(), model.Service{Command: []string{"/bin/sleep", "60"}, Stop: model.StopPolicy{Timeout: time.Second}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		handle.Stop(ctx, model.StopPolicy{Timeout: time.Second})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if result := handle.Stop(ctx, model.StopPolicy{Timeout: time.Second}); !result.Complete {
		t.Fatalf("fresh native stop failed: %+v", result)
	}
	select {
	case <-handle.Done():
	case <-ctx.Done():
		t.Fatal("root wait did not complete")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("managed fresh start/stop invoked ps: %v", err)
	}
}

func TestLegacyDarwinRefusesBlockedPSBeforeLaunchAndDuringCapture(t *testing.T) {
	root := t.TempDir()
	psMarker := filepath.Join(root, "ps-was-invoked")
	launched := filepath.Join(root, "service-was-launched")
	script := "#!/bin/sh\nprintf called > \"$STACKHARBOR_PS_MARKER\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(root, "ps"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("STACKHARBOR_PS_MARKER", psMarker)
	legacy, err := NewLocal(process.NewReader()).Start(context.Background(), model.Service{Command: []string{"/bin/sh", "-c", `printf launched > "$1"`, "fixture", launched}}, nil)
	if legacy != nil || err == nil || !strings.Contains(err.Error(), "service not started") {
		t.Fatalf("legacy preflight bypassed blocked ps: handle=%v err=%v", legacy, err)
	}
	if _, err := os.Stat(psMarker); err != nil {
		t.Fatal("legacy preflight did not invoke its process inspection tool", err)
	}
	if _, err := os.Stat(launched); !os.IsNotExist(err) {
		t.Fatalf("legacy command ran without process inspection: %v", err)
	}
	if err := os.Remove(psMarker); err != nil {
		t.Fatal(err)
	}
	reader := process.NewReader()
	self, err := process.ReadIdentity(context.Background(), reader, int32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	local := &handle{reader: reader, owned: map[int32]model.ProcessIdentity{self.Identity.PID: self.Identity}}
	if err := local.capture(context.Background()); err == nil || !strings.Contains(err.Error(), "descendant cleanup cannot be verified") {
		t.Fatalf("legacy capture bypassed blocked ps: %v", err)
	}
	if _, err := os.Stat(psMarker); err != nil {
		t.Fatal("legacy capture did not invoke its process inspection tool", err)
	}
}
