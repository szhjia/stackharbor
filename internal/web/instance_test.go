package web

import (
	"context"
	"errors"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func namespace(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sh-web-")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
func launch(t *testing.T, o Options) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	o.Ready = func(s string) { ready <- s }
	done := make(chan error, 1)
	go func() { defer close(done); done <- Run(ctx, o) }()
	select {
	case s := <-ready:
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("shutdown timeout")
			}
		})
		return s, cancel, done
	case err := <-done:
		cancel()
		t.Fatalf("launch %v", err)
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("readiness timeout")
	}
	return "", nil, nil
}
func TestWebPortDefaultExplicitZeroAndConflict(t *testing.T) {
	if normalizedPort(Options{}) != 16800 || normalizedPort(Options{PortExplicit: true, Port: 0}) != 0 {
		t.Fatal("port semantics")
	}
	s, _, _ := launch(t, Options{Namespace: namespace(t), PortExplicit: true, Port: 0, NoOpen: true})
	u, _ := url.Parse(s)
	if !strings.HasPrefix(u.Host, "127.0.0.1:") || u.Fragment != "" || u.RawQuery != "" {
		t.Fatalf("url %s", s)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	err = Run(context.Background(), Options{Namespace: namespace(t), PortExplicit: true, Port: l.Addr().(*net.TCPAddr).Port, NoOpen: true})
	if err == nil {
		t.Fatal("port conflict silently drifted")
	}
	for _, port := range []int{-1, 65536} {
		if Run(context.Background(), Options{Namespace: namespace(t), PortExplicit: true, Port: port}) == nil {
			t.Fatal("invalid port accepted")
		}
	}
}
func TestWebReuseAndExplicitMismatch(t *testing.T) {
	ns := namespace(t)
	s, _, _ := launch(t, Options{Namespace: ns, PortExplicit: true, Port: 0, NoOpen: true})
	u, _ := url.Parse(s)
	for _, o := range []Options{{Namespace: ns, NoOpen: true}, {Namespace: ns, NoOpen: true, PortExplicit: true, Port: 0}} {
		var reused string
		o.Ready = func(v string) { reused = v }
		if err := Run(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		v, _ := url.Parse(reused)
		if v.Host != u.Host || v.Fragment != "" {
			t.Fatalf("reuse %s %s", s, reused)
		}
	}
	if err := Run(context.Background(), Options{Namespace: ns, NoOpen: true, PortExplicit: true, Port: 1}); err == nil {
		t.Fatal("mismatch accepted")
	}
}
func TestOpenFailureKeepsServing(t *testing.T) {
	opened := make(chan string, 1)
	s, _, _ := launch(t, Options{Namespace: namespace(t), PortExplicit: true, Port: 0, OpenURL: func(s string) error { opened <- s; return errors.New("no browser") }})
	select {
	case got := <-opened:
		if got != s {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("not opened")
	}
	u, _ := url.Parse(s)
	u.Fragment = ""
	response, err := http.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("real embedded UI must remain available: %d", response.StatusCode)
	}
}
func TestWebShutdownLeavesSessionsAlive(t *testing.T) {
	ns := namespace(t)
	t.Setenv("STACKHARBOR_CACHE_DIR", ns)
	root := t.TempDir()
	h, err := sessionhost.New(context.Background(), model.Workspace{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(context.Background()) })
	_, cancel, done := launch(t, Options{Namespace: ns, PortExplicit: true, Port: 0, NoOpen: true})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	c, err := sessionapi.Connect(context.Background(), h.SessionInfo())
	if err != nil {
		t.Fatalf("session lost: %v", err)
	}
	defer c.Close()
	if _, err := c.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWebNamespacesKeepIndependentBrowserCookies(t *testing.T) {
	a, _, _ := launch(t, Options{Namespace: namespace(t), PortExplicit: true, Port: 0, NoOpen: true})
	b, _, _ := launch(t, Options{Namespace: namespace(t), PortExplicit: true, Port: 0, NoOpen: true})
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	exchange := func(value string) {
		t.Helper()
		u, _ := url.Parse(value)
		u.Fragment = ""
		u.Path = "/api/v1/auth/session"
		req, _ := http.NewRequest("GET", u.String(), nil)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal(response.StatusCode)
		}
	}
	exchange(a)
	exchange(b)
	for _, value := range []string{a, b} {
		u, _ := url.Parse(value)
		u.Fragment = ""
		u.Path = "/api/v1/auth/session"
		response, err := client.Get(u.String())
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("independent session cookie overwritten %d", response.StatusCode)
		}
	}
}
func TestWebAcceptsSafeReadableRegistryCache(t *testing.T) {
	ns := namespace(t)
	if err := os.Chmod(ns, 0755); err != nil {
		t.Fatal(err)
	}
	launch(t, Options{Namespace: ns, PortExplicit: true, Port: 0, NoOpen: true})
}
func TestWebOccupiedUnreachableInstanceNeverStartsSecond(t *testing.T) {
	ns := namespace(t)
	i, _, err := lockInstance(ns)
	if err != nil {
		t.Fatal(err)
	}
	defer i.close()
	i.info.Port = 16800
	if err := i.publish(); err != nil {
		t.Fatal(err)
	}
	opened := false
	err = Run(context.Background(), Options{Namespace: ns, PortExplicit: true, Port: 0, OpenURL: func(string) error { opened = true; return nil }})
	if err == nil || opened {
		t.Fatalf("unreachable owner bypass %v %v", err, opened)
	}
	_, occupied, err := lockInstance(ns)
	if err != nil || occupied == nil {
		t.Fatal("original instance lock lost")
	}
	occupied.Close()
}
func TestWebReuseRejectsHandshakeAndPreservesReplacementSocket(t *testing.T) {
	ns := namespace(t)
	i, _, err := lockInstance(ns)
	if err != nil {
		t.Fatal(err)
	}
	defer i.close()
	i.info.Port = 16800
	g := NewGateway("127.0.0.1:16800", Options{})
	if err := i.control(g); err != nil {
		t.Fatal(err)
	}
	original := i.info.Socket
	renamed := original + ".old"
	if err := os.Rename(original, renamed); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(renamed)
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: original, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	replacement.SetUnlinkOnClose(false)
	defer replacement.Close()
	defer os.Remove(original)
	os.Chmod(original, 0600)
	wrong := i.info
	wrong.ID = "changed"
	var issued atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/credential" {
			issued.Add(1)
		}
		writeJSON(w, 200, wrong, nil)
	})}
	defer server.Close()
	go server.Serve(replacement)
	if err := Run(context.Background(), Options{Namespace: ns, NoOpen: true}); err == nil {
		t.Fatal("mismatched handshake accepted")
	}
	if issued.Load() != 0 {
		t.Fatal("credential minted before identity checked")
	}
	i.close()
	if _, err := os.Lstat(original); err != nil {
		t.Fatal("replacement socket removed")
	}
}
func TestWebNamespaceCanonicalizationAndLongSocketPath(t *testing.T) {
	ns := namespace(t)
	long := filepath.Join(ns, strings.Repeat("x", 90))
	os.MkdirAll(long, 0700)
	canonical, err := namespacePath(long)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := runtimeDirectory(canonical)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if len(filepath.Join(dir, strings.Repeat("x", 32)+".sock")) >= 100 {
		t.Fatal("socket fallback remains too long")
	}
	launch(t, Options{Namespace: long, PortExplicit: true, Port: 0, NoOpen: true})
	unsafe := namespace(t)
	os.Chmod(unsafe, 0777)
	if Run(context.Background(), Options{Namespace: unsafe, PortExplicit: true, Port: 0, NoOpen: true}) == nil {
		t.Fatal("unsafe cache accepted")
	}
}

func TestWebBootstrapFragmentNeverReachesHTTP(t *testing.T) {
	observed := make(chan string, 1)
	s, _, _ := launch(t, Options{Namespace: namespace(t), PortExplicit: true, Port: 0, NoOpen: true, Static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { observed <- r.RequestURI; w.WriteHeader(200) })})
	response, err := http.Get(s)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	uri := <-observed
	if uri != "/" || strings.Contains(uri, "token") || strings.Contains(uri, "#") {
		t.Fatalf("fragment leaked on wire %s", uri)
	}
}
