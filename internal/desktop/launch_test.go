package desktop

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/buildinfo"
)

func TestGraphicalEnvironment(t *testing.T) {
	for _, tc := range []struct {
		goos    string
		env     map[string]string
		allowed bool
	}{
		{"darwin", nil, true}, {"darwin", map[string]string{"SSH_CONNECTION": "ssh"}, false},
		{"linux", nil, false}, {"linux", map[string]string{"DISPLAY": ":1"}, false},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DBUS_SESSION_BUS_ADDRESS": "unix:path=bus"}, true},
		{"windows", nil, false},
	} {
		if err := graphicalEnvironment(tc.goos, func(key string) string { return tc.env[key] }); (err == nil) != tc.allowed {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

func TestMasterSocketChecksVersionAndIdentity(t *testing.T) {
	for _, version := range []string{buildinfo.Version, "old"} {
		dir := testNamespace(t)
		socket := filepath.Join(dir, "master.sock")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(socket, 0600); err != nil {
			t.Fatal(err)
		}
		requests := make(chan Request, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			var request Request
			if json.NewDecoder(conn).Decode(&request) == nil {
				requests <- request
				_ = json.NewEncoder(conn).Encode(Reply{ProtocolVersion: ProtocolVersion, BuildVersion: version})
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = send(ctx, socket, Request{ProtocolVersion: ProtocolVersion, BuildVersion: buildinfo.Version, Namespace: dir, Background: true})
		cancel()
		listener.Close()
		if (err == nil) != (version == buildinfo.Version) {
			t.Fatalf("version %s: %v", version, err)
		}
		select {
		case request := <-requests:
			if !request.Background || request.Namespace != dir {
				t.Fatal(request)
			}
		case <-time.After(time.Second):
			t.Fatal("no request")
		}
	}
	dir := testNamespace(t)
	path := filepath.Join(dir, "master.sock")
	os.WriteFile(path, []byte("preserve"), 0600)
	if send(context.Background(), path, Request{}) == nil {
		t.Fatal("regular file accepted as socket")
	}
	if data, _ := os.ReadFile(path); string(data) != "preserve" {
		t.Fatal("foreign file modified")
	}
}

func TestMasterEnvironmentDoesNotInheritProjectSecretsOrNamespace(t *testing.T) {
	t.Setenv("STACKHARBOR_CACHE_DIR", "/project/private")
	t.Setenv("STACKHARBOR_TEST_SECRET", "do-not-forward")
	env := strings.Join(desktopEnvironment("/runtime"), "\n")
	if strings.Contains(env, "do-not-forward") || strings.Contains(env, "STACKHARBOR_CACHE_DIR=") {
		t.Fatal("project environment was forwarded")
	}
	if !strings.Contains(env, "STACKHARBOR_DESKTOP_RUNTIME_DIR=/runtime") {
		t.Fatal("runtime missing")
	}
}

func TestExplicitDesktopExecutableMustBeAbsoluteAndExecutable(t *testing.T) {
	t.Setenv("STACKHARBOR_DESKTOP_APP", "relative")
	if _, err := installedExecutable(); err == nil {
		t.Fatal("relative override accepted")
	}
	path := filepath.Join(t.TempDir(), "desktop")
	os.WriteFile(path, []byte("not executable"), 0600)
	t.Setenv("STACKHARBOR_DESKTOP_APP", path)
	if _, err := installedExecutable(); err == nil {
		t.Fatal("nonexecutable override accepted")
	}
}

func TestLaunchRetriesNativeForwarderThatExitsBeforeEndpointExists(t *testing.T) {
	dir := testNamespace(t)
	app := filepath.Join(dir, "desktop")
	marker := filepath.Join(dir, "first-launch-exited")
	socket := filepath.Join(dir, "master.sock")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	body := "#!/bin/sh\nif [ \"$1\" = --desktop-version ]; then printf '%s\\n' " + quote(buildinfo.Version) + "; exit 0; fi\n" +
		"if [ ! -e " + quote(marker) + " ]; then : > " + quote(marker) + "; exit 0; fi\n" +
		"SH_DESKTOP_RETRY_SOCKET=" + quote(socket) + " exec " + quote(self) + " -test.run=^TestDesktopLaunchRetryFixture$\n"
	if err := os.WriteFile(app, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"SSH_CONNECTION": "", "SSH_TTY": "", "DISPLAY": ":fixture", "DBUS_SESSION_BUS_ADDRESS": "fixture", "STACKHARBOR_DESKTOP_APP": app, "STACKHARBOR_DESKTOP_RUNTIME_DIR": dir, "STACKHARBOR_CACHE_DIR": dir} {
		t.Setenv(key, value)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Launch(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("first native forwarding process did not exit", err)
	}
}

func TestDesktopLaunchRetryFixture(t *testing.T) {
	socket := os.Getenv("SH_DESKTOP_RETRY_SOCKET")
	if socket == "" {
		return
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.Exit(2)
	}
	if err := os.Chmod(socket, 0600); err != nil {
		os.Exit(2)
	}
	count, _ := strconv.Atoi(os.Getenv("SH_DESKTOP_REQUEST_COUNT"))
	if count == 0 {
		count = 1
	}
	for i := 0; i < count; i++ {
		conn, err := listener.Accept()
		if err != nil {
			os.Exit(2)
		}
		var request Request
		if json.NewDecoder(conn).Decode(&request) != nil || !request.Background {
			os.Exit(2)
		}
		if json.NewEncoder(conn).Encode(Reply{ProtocolVersion: ProtocolVersion, BuildVersion: buildinfo.Version}) != nil {
			os.Exit(2)
		}
		conn.Close()
	}
	listener.Close()
	os.Exit(0)
}

func TestConcurrentColdLaunchesStartOneNativeProcess(t *testing.T) {
	dir := testNamespace(t)
	app, marker := filepath.Join(dir, "desktop"), filepath.Join(dir, "launched")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	body := "#!/bin/sh\nif [ \"$1\" = --desktop-version ]; then printf '%s\\n' " + quote(buildinfo.Version) + "; exit 0; fi\n" +
		"printf 'launch\\n' >> " + quote(marker) + "\n" +
		"SH_DESKTOP_REQUEST_COUNT=2 SH_DESKTOP_RETRY_SOCKET=" + quote(filepath.Join(dir, "master.sock")) + " exec " + quote(self) + " -test.run=^TestDesktopLaunchRetryFixture$\n"
	if err := os.WriteFile(app, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"SSH_CONNECTION": "", "SSH_TTY": "", "DISPLAY": ":fixture", "DBUS_SESSION_BUS_ADDRESS": "fixture", "STACKHARBOR_DESKTOP_APP": app, "STACKHARBOR_DESKTOP_RUNTIME_DIR": dir, "STACKHARBOR_CACHE_DIR": dir} {
		t.Setenv(key, value)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- Launch(ctx, true) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "launch\n" {
		t.Fatalf("multiple native processes launched: %q %v", data, err)
	}
}
