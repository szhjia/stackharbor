package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/buildinfo"
)

func nativeLaunchFixture(t *testing.T, version string) (string, string, string) {
	t.Helper()
	dir := testNamespace(t)
	bundle := filepath.Join(dir, "Stack Harbor.app")
	app := filepath.Join(bundle, "Contents", "MacOS", "stackharbor-desktop")
	if err := os.MkdirAll(filepath.Dir(app), 0700); err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	// Probing is the only permitted direct execution; it receives no project
	// secret and exits before any native initialization.
	body := "#!/bin/sh\n[ -z \"${STACKHARBOR_TEST_SECRET-}\" ] || exit 22\nif [ \"$1\" = --desktop-version ]; then printf '%s\\n' " + quote(version) + "; exit 0; fi\nexit 23\n"
	if err := os.WriteFile(app, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(dir, "launch-args")
	opener := filepath.Join(dir, "open")
	body = "#!/bin/sh\n[ -z \"${STACKHARBOR_TEST_SECRET-}\" ] || exit 24\nprintf '%s\\n' \"$@\" > " + quote(argsFile) + "\n"
	if err := os.WriteFile(opener, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STACKHARBOR_DESKTOP_APP", app)
	t.Setenv("STACKHARBOR_TEST_SECRET", "project-secret-do-not-forward")
	t.Setenv("STACKHARBOR_DESKTOP_RUNTIME_DIR", filepath.Join(dir, "unused-legacy-runtime"))
	ns := filepath.Join(dir, "namespace")
	if err := os.Mkdir(ns, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STACKHARBOR_CACHE_DIR", ns)
	return bundle, opener, argsFile
}

func TestMacOSLaunchHandsOffThroughNativeSingletonWithoutLegacySocket(t *testing.T) {
	bundle, opener, argsFile := nativeLaunchFixture(t, buildinfo.Version)
	for _, background := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := launchMacOS(ctx, background, opener)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(args) < 3 || args[0] != "-n" || args[1] != "-a" || args[2] != bundle {
			t.Fatal("bundle was not handed to LaunchServices as an argument", args)
		}
		if strings.Contains(string(data), "-W\n") {
			t.Fatal("launch waits for native shell exit", args)
		}
		if background {
			if len(args) != 8 || args[3] != "-g" || args[4] != "--args" || args[5] != "--namespace" || args[6] != os.Getenv("STACKHARBOR_CACHE_DIR") || args[7] != "--background" {
				t.Fatal("background intent not preserved", args)
			}
		} else if len(args) != 6 || args[3] != "--args" || args[4] != "--namespace" || args[5] != os.Getenv("STACKHARBOR_CACHE_DIR") {
			t.Fatal("foreground launch lost selected namespace or requested hidden shell", args)
		}
		if _, err = os.Stat(os.Getenv("STACKHARBOR_DESKTOP_RUNTIME_DIR")); !os.IsNotExist(err) {
			t.Fatal("modern launch touched legacy runtime", err)
		}
	}
}

func TestMacOSLaunchPreservesVersionAndBundleChecks(t *testing.T) {
	_, opener, argsFile := nativeLaunchFixture(t, "old-build")
	if err := launchMacOS(context.Background(), false, opener); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatal("incompatible app launched", err)
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatal("version rejection reached LaunchServices", err)
	}
	raw := filepath.Join(t.TempDir(), "desktop")
	if err := os.WriteFile(raw, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STACKHARBOR_DESKTOP_APP", raw)
	if err := launchMacOS(context.Background(), false, opener); err == nil || !strings.Contains(err.Error(), ".app bundle") {
		t.Fatal("unbundled executable sent to LaunchServices", err)
	}
}
