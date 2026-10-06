package web

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedActualUIAndSPAFallback(t *testing.T) {
	h := Assets()
	for _, path := range []string{"/", "/workspaces", "/resources", "/operations", "/workspaces/session", "/workspaces/session/tasks", "/workspaces/session/resources", "/workspaces/session/logs?target=backend%2Fapi"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "/assets/") {
			t.Fatalf("actual built entry %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	data, err := fs.ReadFile(assetFiles, "dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	asset := regexp.MustCompile(`src="([^"]+\.js)"`).FindStringSubmatch(string(data))
	if len(asset) != 2 {
		t.Fatal("no real bundled script")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", asset[1], nil))
	if w.Code != 200 || w.Body.Len() < 10000 {
		t.Fatal("missing actual React bundle")
	}
	for _, path := range []string{"/api/v1/unknown", "/assets/missing.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "<html") {
			t.Fatalf("invalid fallback %s %d", path, w.Code)
		}
	}
}
func TestProductionAndExactDevelopmentOrigin(t *testing.T) {
	for _, tc := range []struct {
		dev, origin string
		want        int
	}{{"", "http://127.0.0.1:5173", 403}, {"http://127.0.0.1:5173", "http://127.0.0.1:5173", 200}, {"http://127.0.0.1:5173", "http://127.0.0.1:5174", 403}, {"http://evil.test:5173", "http://evil.test:5173", 403}} {
		g := NewGateway("127.0.0.1:16800", Options{DevelopmentOrigin: tc.dev})
		token, _ := g.auth.issue()
		r := httptest.NewRequest("POST", "http://127.0.0.1:16800/api/v1/auth/exchange", strings.NewReader(`{"token":"`+token+`"}`))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%+v got %d", tc, w.Code)
		}
	}
}

func TestDevelopmentBootstrapAndInvalidOrigin(t *testing.T) {
	var output bytes.Buffer
	show(Options{NoOpen: true, DevelopmentOrigin: "http://127.0.0.1:5173", Out: &output}, bootstrapURL(16800, "fixture-token"))
	if output.String() != "http://127.0.0.1:5173/#token=fixture-token\n" {
		t.Fatal(output.String())
	}
	for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:5173/", "http://127.0.0.1:5173?q=1", "https://127.0.0.1:5173", "http://remote:5173", "http://127.0.0.1:0"} {
		if err := Run(context.Background(), Options{DevelopmentOrigin: origin}); err == nil {
			t.Fatalf("invalid development origin accepted %s", origin)
		}
	}
}

func TestGatewayUnknownAPINeverFallsBackHTML(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	cookie, _ := authenticated(t, g)
	w := request(g, "GET", "/api/v1/unknown", "", g.host, "", cookie, "")
	if w.Code != 404 || strings.Contains(w.Body.String(), "<html") || !strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Fatalf("API fallback %d %s", w.Code, w.Body.String())
	}
}

func TestDevelopmentReuseCannotReconfigureProductionOwner(t *testing.T) {
	ns := namespace(t)
	_, cancel, done := launch(t, Options{Namespace: ns, PortExplicit: true, Port: 0, NoOpen: true})
	defer func() { cancel(); <-done }()
	err := Run(context.Background(), Options{Namespace: ns, PortExplicit: true, Port: 0, NoOpen: true, DevelopmentOrigin: "http://127.0.0.1:5173"})
	if err == nil || !strings.Contains(err.Error(), "development origin") {
		t.Fatalf("production owner reconfigured: %v", err)
	}
}

func TestDevelopmentOwnerSameModeAndOmittedFlagReuse(t *testing.T) {
	ns := namespace(t)
	_, cancel, done := launch(t, Options{Namespace: ns, PortExplicit: true, Port: 0, NoOpen: true, DevelopmentOrigin: "http://127.0.0.1:5173"})
	defer func() { cancel(); <-done }()
	for _, dev := range []string{"http://127.0.0.1:5173", ""} {
		var ready string
		err := Run(context.Background(), Options{Namespace: ns, PortExplicit: true, Port: 0, NoOpen: true, DevelopmentOrigin: dev, Ready: func(s string) { ready = s }})
		if err != nil {
			t.Fatal(err)
		}
		if dev != "" && ready != dev+"/" {
			t.Fatal(ready)
		}
		if dev == "" && strings.HasPrefix(ready, "http://127.0.0.1:5173/") {
			t.Fatal("omitted development flag did not show actual gateway")
		}
	}
}
