package observe

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
)

func TestNoPortsOrProcessesDoesNotScanUnrelatedListeners(t *testing.T) {
	dir := t.TempDir()
	called := filepath.Join(dir, "called")
	script := "#!/bin/sh\nprintf called > '" + called + "'\nexit 2\n"
	if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if rows := NewPortProbe().Observe(context.Background(), nil, nil); len(rows) != 0 {
		t.Fatal("empty scope must not expose unrelated ports", rows)
	}
	if _, err := os.Stat(called); !os.IsNotExist(err) {
		t.Fatal("empty scope invoked the system listener scanner", err)
	}
}

func TestMacNumericScanRetainsIPv4AndIPv6ListenerIdentity(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific nonblocking socket scan")
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Fatal(err)
	}
	var ports []model.Port
	for _, address := range []struct{ network, host string }{{"tcp4", "127.0.0.1:0"}, {"tcp6", "[::1]:0"}} {
		listener, err := net.Listen(address.network, address.host)
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		ports = append(ports, model.Port{Number: listener.Addr().(*net.TCPAddr).Port})
	}
	rows := NewPortProbe().Observe(context.Background(), ports, nil)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	for _, row := range rows {
		if row.Status != "external" || len(row.Listeners) == 0 || row.Listeners[0].PID != int32(os.Getpid()) {
			t.Fatal("nonblocking scan lost socket or owner", row)
		}
	}
}

func slowLsof(t *testing.T, output string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n/bin/sleep 2.2\nprintf '" + output + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestPortsAllowSlowCompleteListenerScan(t *testing.T) {
	slowLsof(t, "p123\\ncserver\\nn127.0.0.1:8793\\n")
	rows := NewPortProbe().Observe(context.Background(), []model.Port{{Number: 8793}}, nil)
	if len(rows) != 1 || rows[0].Status != "external" || len(rows[0].Listeners) != 1 || rows[0].Listeners[0].PID != 123 {
		t.Fatal("slow complete scan must retain listener identity", rows)
	}
}

func TestSlowScanStartPlanPreservesExternalConflict(t *testing.T) {
	slowLsof(t, "p123\\ncserver\\nn127.0.0.1:8793\\n")
	conflicts, err := FindConflicts(context.Background(), NewPortProbe(), metricReader{}, []model.Port{{Number: 8793}}, nil)
	if err != nil || len(conflicts) != 1 || conflicts[0].Port != 8793 || conflicts[0].Identity.PID != 123 {
		t.Fatal("slow scan must expose conflicts to the start plan", conflicts, err)
	}
}

func TestPortScanHonorsEarlierCallerDeadline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	rows := NewPortProbe().Observe(ctx, []model.Port{{Number: 8793}}, nil)
	if time.Since(started) > time.Second || len(rows) != 1 || rows[0].Status != "unknown" || rows[0].Reason == "" {
		t.Fatal("caller deadline must remain bounded and fail closed", rows)
	}
	_, err := FindConflicts(ctx, NewPortProbe(), metricReader{}, []model.Port{{Number: 8793}}, nil)
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatal("expired plan context must not permit a start", err)
	}
}
