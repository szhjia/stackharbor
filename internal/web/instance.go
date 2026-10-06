package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	urlpkg "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

const instanceProtocol = 1

type instanceInfo struct {
	DevelopmentOrigin string `json:"development_origin,omitempty"`
	ID                string `json:"id"`
	Namespace         string `json:"namespace"`
	PID               int    `json:"pid"`
	CreatedMillis     int64  `json:"created_millis"`
	Port              int    `json:"port"`
	Socket            string `json:"socket"`
	Protocol          int    `json:"protocol"`
}
type instance struct {
	file     *os.File
	info     instanceInfo
	server   *http.Server
	listener *net.UnixListener
	inode    os.FileInfo
}

func normalizedPort(o Options) int {
	if !o.PortExplicit {
		return 16800
	}
	return o.Port
}
func namespacePath(namespace string) (string, error) {
	if namespace == "" {
		namespace = os.Getenv("STACKHARBOR_CACHE_DIR")
	}
	if namespace == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		namespace = filepath.Join(base, "stackharbor")
	}
	abs, err := filepath.Abs(namespace)
	if err != nil {
		return "", err
	}
	st, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		if err = supervisor.EnsurePrivateDirectory(abs); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else {
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !st.IsDir() || !ok || int(owner.Uid) != os.Getuid() || st.Mode().Perm()&0022 != 0 {
			return "", apiError("forbidden", "unsafe Web registry cache")
		}
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return real, nil
}
func runtimeDirectory(cache string) (string, error) {
	dir := filepath.Join(cache, "web-run")
	if len(filepath.Join(dir, "01234567890123456789012345678901.sock")) >= 100 {
		base, err := filepath.EvalSymlinks("/tmp")
		if err != nil {
			return "", err
		}
		hash := sha256.Sum256([]byte(cache))
		dir = filepath.Join(base, fmt.Sprintf("sh-web-%d-%x", os.Getuid(), hash[:8]))
	}
	if err := supervisor.EnsurePrivateDirectory(dir); err != nil {
		return "", err
	}
	if err := sessionapi.ValidatePrivateDirectory(dir); err != nil {
		return "", err
	}
	return dir, nil
}
func privateFile(st os.FileInfo) bool {
	owner, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(owner.Uid) == os.Getuid() && st.Mode().IsRegular() && st.Mode().Perm() == 0600
}
func lockInstance(cache string) (*instance, *os.File, error) {
	dir, err := runtimeDirectory(cache)
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, "web.lock")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	st, err := file.Stat()
	if err != nil || !privateFile(st) {
		file.Close()
		return nil, nil, apiError("forbidden", "unsafe Web instance lock")
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, file, nil
		}
		file.Close()
		return nil, nil, err
	}
	id, err := randomToken()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	p, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	birth, err := p.CreateTime()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	hash := sha256.Sum256([]byte(cache))
	info := instanceInfo{ID: id, Namespace: fmt.Sprintf("%x", hash), PID: os.Getpid(), CreatedMillis: birth, Socket: filepath.Join(dir, id[:32]+".sock"), Protocol: instanceProtocol}
	return &instance{file: file, info: info}, nil, nil
}
func (i *instance) publish() error {
	st, err := i.file.Stat()
	if err != nil {
		return err
	}
	named, err := os.Lstat(i.file.Name())
	if err != nil || !os.SameFile(st, named) || !privateFile(named) {
		return apiError("identity_conflict", "Web instance lock changed")
	}
	data, err := json.Marshal(i.info)
	if err != nil {
		return err
	}
	n, err := i.file.WriteAt(data, 0)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	if err = i.file.Truncate(int64(len(data))); err != nil {
		return err
	}
	return i.file.Sync()
}
func (i *instance) control(g *Gateway) error {
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: i.info.Socket, Net: "unix"})
	if err != nil {
		return err
	}
	l.SetUnlinkOnClose(false)
	i.listener = l
	i.inode, err = os.Lstat(i.info.Socket)
	if err != nil {
		return err
	}
	if err = os.Chmod(i.info.Socket, 0600); err != nil {
		return err
	}
	if _, err = sessionapi.ValidatePrivateSocket(i.info.Socket); err != nil {
		return err
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/identity":
			if method(w, r, "GET") {
				writeJSON(w, 200, i.info, nil)
			}
		case "/v1/credential":
			if !method(w, r, "POST") {
				return
			}
			var expected instanceInfo
			if err := decode(w, r, &expected); err != nil {
				fail(w, err)
				return
			}
			if expected != i.info {
				fail(w, apiError("identity_conflict", "Web gateway identity changed"))
				return
			}
			token, err := g.auth.issue()
			if err != nil {
				fail(w, err)
				return
			}
			writeJSON(w, 200, struct {
				Token string `json:"token"`
			}{token}, nil)
		default:
			fail(w, apiError("not_found", "route not found"))
		}
	})
	i.server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	go i.server.Serve(l)
	return i.publish()
}
func (i *instance) close() {
	if i.server != nil {
		i.server.Close()
	}
	if i.listener != nil {
		i.listener.Close()
	}
	if i.inode != nil {
		if named, err := os.Lstat(i.info.Socket); err == nil && os.SameFile(i.inode, named) {
			os.Remove(i.info.Socket)
		}
	}
	i.file.Close()
}
func readInstance(ctx context.Context, file *os.File, cache string) (instanceInfo, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		var info instanceInfo
		st, err := file.Stat()
		if err == nil && st.Size() > 0 && st.Size() <= 16384 {
			err = json.NewDecoder(io.NewSectionReader(file, 0, 16384)).Decode(&info)
			if err == nil {
				hash := sha256.Sum256([]byte(cache))
				dir, dirErr := runtimeDirectory(cache)
				if dirErr != nil {
					return info, dirErr
				}
				_, idErr := hex.DecodeString(info.ID)
				if len(info.ID) != 64 || idErr != nil || info.Namespace != fmt.Sprintf("%x", hash) || info.PID <= 0 || info.CreatedMillis <= 0 || info.Protocol != instanceProtocol || info.DevelopmentOrigin != "" && !validDevelopmentOrigin(info.DevelopmentOrigin) || info.Port < 1 || info.Port > 65535 || info.Socket != filepath.Join(dir, info.ID[:min(32, len(info.ID))]+".sock") {
					return info, apiError("identity_conflict", "invalid Web gateway registration")
				}
				return info, nil
			}
		}
		if time.Now().After(deadline) {
			return info, apiError("unavailable", "Web gateway lock is occupied but registration is unavailable")
		}
		select {
		case <-ctx.Done():
			return info, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func reuse(ctx context.Context, file *os.File, cache string, o Options) (string, error) {
	defer file.Close()
	info, err := readInstance(ctx, file, cache)
	if err != nil {
		return "", err
	}
	st, err := file.Stat()
	named, namedErr := os.Lstat(file.Name())
	if err != nil || namedErr != nil || !os.SameFile(st, named) || !privateFile(named) {
		return "", apiError("identity_conflict", "Web instance lock replaced")
	}
	p, err := process.NewProcessWithContext(ctx, int32(info.PID))
	if err != nil {
		return "", apiError("identity_conflict", "Web gateway owner missing")
	}
	birth, err := p.CreateTimeWithContext(ctx)
	if err != nil || birth != info.CreatedMillis {
		return "", apiError("identity_conflict", "Web gateway owner changed")
	}
	inode, err := sessionapi.ValidatePrivateSocket(info.Socket)
	if err != nil {
		return "", err
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		named, err := sessionapi.ValidatePrivateSocket(info.Socket)
		if err != nil || !os.SameFile(inode, named) {
			return nil, apiError("identity_conflict", "Web control endpoint changed")
		}
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", info.Socket)
		if err != nil {
			return nil, err
		}
		named, err = sessionapi.ValidatePrivateSocket(info.Socket)
		if err != nil || !os.SameFile(inode, named) {
			conn.Close()
			return nil, apiError("identity_conflict", "Web control endpoint replaced")
		}
		return conn, nil
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	rpc := func(method, path string, input, output any) error {
		var body io.Reader
		if input != nil {
			data, err := json.Marshal(input)
			if err != nil {
				return err
			}
			body = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(ctx, method, "http://web"+path, body)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var envelope sessionapi.Response[json.RawMessage]
		if err = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&envelope); err != nil {
			return err
		}
		if envelope.Error != nil {
			return envelope.Error
		}
		if resp.StatusCode != 200 {
			return apiError("unavailable", "unexpected Web control response")
		}
		return json.Unmarshal(envelope.Data, output)
	}
	var handshake instanceInfo
	if err = rpc("GET", "/v1/identity", nil, &handshake); err != nil {
		return "", err
	}
	if handshake != info {
		return "", apiError("identity_conflict", "Web control handshake mismatch")
	}
	if o.PortExplicit && o.Port != 0 && o.Port != info.Port {
		return "", apiError("endpoint_conflict", fmt.Sprintf("Web already serves port %d, requested %d", info.Port, o.Port))
	}
	if o.DevelopmentOrigin != "" && o.DevelopmentOrigin != info.DevelopmentOrigin {
		return "", apiError("endpoint_conflict", "existing gateway has a different development origin; stop it and restart with --dev-origin")
	}
	return bootstrapURL(info.Port, ""), nil
}
func bootstrapURL(port int, token string) string {
	if token == "" {
		return "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	}
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/#token=" + token
}
func openBrowser(url string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, command, url).Run()
}
func show(o Options, url string) {
	if o.DevelopmentOrigin != "" {
		if parsed, err := urlpkg.Parse(url); err == nil {
			url = o.DevelopmentOrigin + "/"
			if parsed.Fragment != "" {
				url += "#" + parsed.Fragment
			}
		}
	}
	if o.Out != nil {
		fmt.Fprintln(o.Out, url)
	}
	if o.Ready != nil {
		o.Ready(url)
	}
	if !o.NoOpen {
		open := o.OpenURL
		if open == nil {
			open = openBrowser
		}
		if err := open(url); err != nil && o.ErrOut != nil {
			fmt.Fprintf(o.ErrOut, "Browser open failed: %v. Use the URL above.\n", err)
		}
	}
}

// Run reuses a verified owner or serves in the foreground. Cancellation closes
// only gateway transports; independently owned sessions are never shut down.
func Run(ctx context.Context, o Options) error {
	if o.DevelopmentOrigin != "" && !validDevelopmentOrigin(o.DevelopmentOrigin) {
		return apiError("invalid_request", "development origin must be an exact http://127.0.0.1:PORT origin")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	port := normalizedPort(o)
	if port < 0 || port > 65535 {
		return apiError("invalid_request", "port must be between 0 and 65535")
	}
	cache, err := namespacePath(o.Namespace)
	if err != nil {
		return err
	}
	o.Namespace = cache
	i, occupied, err := lockInstance(cache)
	if err != nil {
		return err
	}
	if occupied != nil {
		url, err := reuse(ctx, occupied, cache, o)
		if err != nil {
			return err
		}
		show(o, url)
		return nil
	}
	defer i.close()
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("listen on requested Web port %d: %w", port, err)
	}
	defer listener.Close()
	i.info.Port = listener.Addr().(*net.TCPAddr).Port
	i.info.DevelopmentOrigin = o.DevelopmentOrigin
	g := NewGateway(listener.Addr().String(), o)
	liveCtx, stopLive := context.WithCancel(ctx)
	defer stopLive()
	g.Start(liveCtx)
	server := &http.Server{Handler: g, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if err = i.control(g); err != nil {
		server.Close()
		return err
	}
	show(o, bootstrapURL(i.info.Port, ""))
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-done:
	}
	stopLive() // Cancel streams before HTTP shutdown; browser streams cannot delay teardown.
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = server.Shutdown(shutdown); err != nil {
		server.Close()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return err
}
