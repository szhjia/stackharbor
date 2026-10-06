package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalBrowserBootstrapsWithoutLaunchCredential(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	for _, cookie := range []string{"", g.auth.cookieName + "=expired"} {
		w := request(g, "GET", "/api/v1/auth/session", "", g.host, "", cookie, "")
		if w.Code != http.StatusOK || len(w.Result().Cookies()) != 1 {
			t.Fatalf("local browser could not connect directly: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestCrossSiteCannotBootstrapLocalBrowser(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	r := httptest.NewRequest("GET", "http://"+g.host+"/api/v1/auth/session", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 {
		t.Fatalf("cross-site bootstrap accepted: %d", w.Code)
	}
}

func TestLocalGatewayPrintsReusablePlainURL(t *testing.T) {
	ns := namespace(t)
	value, _, _ := launch(t, Options{Namespace: ns, PortExplicit: true, Port: 0, NoOpen: true})
	var reopened string
	if err := Run(context.Background(), Options{Namespace: ns, NoOpen: true, Ready: func(v string) { reopened = v }}); err != nil {
		t.Fatal(err)
	}
	if value != reopened || strings.Contains(value, "#") || strings.Contains(value, "token") {
		t.Fatalf("expected stable ordinary URL: %s %s", value, reopened)
	}
}
