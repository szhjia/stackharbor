//go:build darwin

package process

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDarwinSharedMetricsAndIdentityDoNotSpawnPS(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ps-was-invoked")
	script := "#!/bin/sh\nprintf called > \"$STACKHARBOR_PS_MARKER\"\nexec /bin/ps \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "ps"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("STACKHARBOR_PS_MARKER", marker)
	pid := int32(os.Getpid())
	identity, err := ReadIdentity(context.Background(), NewReader(), pid)
	if err != nil {
		t.Fatal(err)
	}
	shared := NewSharedReader(NewReader(), func() time.Duration { return time.Hour })
	info, err := shared.Read(context.Background(), pid)
	if err != nil || info.Identity != identity.Identity || !info.MemoryKnown || !info.CPUKnown {
		t.Fatalf("metric contract lost: %+v %v", info, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("hidden per-PID ps helper ran: %v", err)
	}
}
