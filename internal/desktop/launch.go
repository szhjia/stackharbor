package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"github.com/szhjia/stackharbor/internal/web"
)

func graphicalEnvironment(goos string, getenv func(string) string) error {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return errors.New("desktop is unavailable in SSH; continuing with the terminal")
	}
	if goos == "linux" && ((getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "") || getenv("DBUS_SESSION_BUS_ADDRESS") == "") {
		return errors.New("desktop requires a graphical login and session bus; continuing with the terminal")
	}
	if goos != "darwin" && goos != "linux" {
		return errors.New("desktop currently supports macOS and Linux")
	}
	return nil
}

func installedExecutable() (string, error) {
	if override := os.Getenv("STACKHARBOR_DESKTOP_APP"); override != "" {
		if !filepath.IsAbs(override) {
			return "", errors.New("STACKHARBOR_DESKTOP_APP must be an absolute executable path")
		}
		return executable(override)
	}
	candidates := []string{}
	if self, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(self); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(real), "stackharbor-desktop"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if runtime.GOOS == "darwin" {
			candidates = append(candidates, filepath.Join(home, "Applications", "StackHarbor.app", "Contents", "MacOS", "stackharbor-desktop"))
		} else {
			candidates = append(candidates, filepath.Join(home, ".local", "bin", "stackharbor-desktop"))
		}
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/Applications/StackHarbor.app/Contents/MacOS/stackharbor-desktop")
	} else {
		candidates = append(candidates, "/usr/bin/stackharbor-desktop", "/usr/local/bin/stackharbor-desktop")
	}
	for _, path := range candidates {
		if found, err := executable(path); err == nil {
			return found, nil
		}
	}
	return "", errors.New("desktop app is not installed; the standalone CLI remains available")
}

func executable(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(real)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("desktop executable is unavailable: %s", path)
	}
	return real, nil
}

func runtimeDirectory() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "stackharbor", "desktop-run")
	if override := os.Getenv("STACKHARBOR_DESKTOP_RUNTIME_DIR"); override != "" {
		if !filepath.IsAbs(override) {
			return "", errors.New("desktop runtime directory must be absolute")
		}
		dir = override
	}
	if err := supervisor.EnsurePrivateDirectory(dir); err != nil {
		return "", err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if err := sessionapi.ValidatePrivateDirectory(dir); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}

func namespace() (string, error) {
	return web.ResolveNamespace("")
}

func send(ctx context.Context, socket string, request Request) error {
	inode, err := sessionapi.ValidatePrivateSocket(socket)
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	if current, err := sessionapi.ValidatePrivateSocket(socket); err != nil || !os.SameFile(inode, current) {
		return errors.New("desktop control socket changed")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return err
	}
	var reply Reply
	if err := json.NewDecoder(io.LimitReader(conn, 16384)).Decode(&reply); err != nil {
		return err
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	if reply.ProtocolVersion != ProtocolVersion || reply.BuildVersion != buildinfo.Version {
		return errors.New("running desktop is incompatible; quit it and install matching desktop/CLI builds")
	}
	return nil
}

// Launch is deliberately optional: callers keep their session even on failure.
// No workspace child processes, project environment or startup commands are
// passed to the desktop; it discovers the session through the existing registry.
func Launch(ctx context.Context, background bool) error {
	if err := graphicalEnvironment(runtime.GOOS, os.Getenv); err != nil {
		return err
	}
	dir, err := runtimeDirectory()
	if err != nil {
		return err
	}
	ns, err := namespace()
	if err != nil {
		return err
	}
	request := Request{ProtocolVersion: ProtocolVersion, BuildVersion: buildinfo.Version, Namespace: ns, Background: background}
	socket := filepath.Join(dir, "master.sock")
	budget, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := send(budget, socket, request); err == nil {
		return nil
	} else if !os.IsNotExist(err) && !errors.Is(err, syscall.ECONNREFUSED) {
		return err
	}
	// Serialize cold startup before initializing AppKit/GTK. Tauri's native
	// singleton still handles launcher/Dock invocations, but simultaneous CLI
	// sessions must not each start a GUI process while its endpoint is pending.
	fd, err := syscall.Open(filepath.Join(dir, "launch.lock"), syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	lock := os.NewFile(uintptr(fd), "desktop-launch")
	defer lock.Close()
	st, err := lock.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return errors.New("unsafe desktop launch lock")
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return errors.New("desktop launch lock belongs to another user")
	}
	for {
		if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			break
		} else if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		if err := send(budget, socket, request); err == nil {
			return nil
		} else if !os.IsNotExist(err) && !errors.Is(err, syscall.ECONNREFUSED) {
			return err
		}
		select {
		case <-budget.Done():
			return fmt.Errorf("desktop startup timed out: %w", budget.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	if err := send(budget, socket, request); err == nil {
		return nil
	} else if !os.IsNotExist(err) && !errors.Is(err, syscall.ECONNREFUSED) {
		return err
	}
	app, err := installedExecutable()
	if err != nil {
		return err
	}
	// Version probing exits before Tauri initialization, so it cannot create a
	// second instance or deliver incompatible arguments to an existing master.
	probe := exec.CommandContext(budget, app, "--desktop-version")
	version, err := probe.Output()
	if err != nil || strings.TrimSpace(string(version)) != buildinfo.Version {
		return errors.New("installed desktop is incompatible; install matching desktop/CLI builds")
	}
	spawn := func() (<-chan error, error) {
		env := desktopEnvironment(dir)
		cmd := exec.Command(app, "--background")
		bundle := filepath.Dir(filepath.Dir(filepath.Dir(app)))
		if runtime.GOOS == "darwin" && filepath.Base(filepath.Dir(app)) == "MacOS" && strings.HasSuffix(bundle, ".app") {
			// LaunchServices completes the native application's launch event
			// sequence. Directly executing a bundle's Mach-O can leave AppKit
			// waiting for launch events during rapid quit/reopen cycles.
			// Keep the launcher alive until its native instance exits. An
			// asynchronous `open` success is not a failed/forwarded instance;
			// treating it as one would launch extra copies while AppKit starts.
			args := []string{"-W", "-n", "-g", "-j", "-a", bundle}
			for _, entry := range env {
				args = append(args, "--env", entry)
			}
			args = append(args, "--args", "--background")
			cmd = exec.Command("/usr/bin/open", args...)
		}
		cmd.Env = env
		cmd.Dir = filepath.Dir(app)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		return exited, nil
	}
	exited, err := spawn()
	if err != nil {
		return err
	}
	attempts := 1
	var retry <-chan time.Time
	for {
		if err := send(budget, socket, request); err == nil {
			return nil
		} else if !os.IsNotExist(err) && !errors.Is(err, syscall.ECONNREFUSED) {
			return err
		}
		select {
		case <-budget.Done():
			return fmt.Errorf("desktop startup timed out: %w", budget.Err())
		case <-exited:
			exited = nil
			// The native singleton can still belong to a master completing
			// shutdown after its control socket has closed. A forwarded child
			// then exits before the replacement endpoint exists. Retry actual
			// launches with bounded backoff, while still polling a concurrent
			// winner's endpoint between attempts.
			if attempts < 4 {
				retry = time.After(time.Duration(attempts) * time.Second)
			}
		case <-retry:
			retry = nil
			if err := budget.Err(); err != nil {
				return fmt.Errorf("desktop startup timed out: %w", err)
			}
			exited, err = spawn()
			if err != nil {
				return err
			}
			attempts++
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func desktopEnvironment(dir string) []string {
	// In particular, do not inherit a project's namespace as the app's default
	// namespace or expose its unrelated secrets to the desktop process.
	allowed := map[string]bool{"HOME": true, "USER": true, "LOGNAME": true, "PATH": true,
		"TMPDIR": true, "LANG": true, "LC_ALL": true, "DISPLAY": true, "WAYLAND_DISPLAY": true,
		"DBUS_SESSION_BUS_ADDRESS": true, "XDG_RUNTIME_DIR": true, "XDG_CACHE_HOME": true,
		"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XAUTHORITY": true}
	env := []string{"STACKHARBOR_DESKTOP_RUNTIME_DIR=" + dir}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if allowed[key] {
			env = append(env, entry)
		}
	}
	return env
}
