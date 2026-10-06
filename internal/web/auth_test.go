package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/inventory"
)

func request(g *Gateway, method, path, body, host, origin, cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+path, bytes.NewBufferString(body))
	r.Host = host
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}
func authenticated(t *testing.T, g *Gateway) (string, string) {
	t.Helper()
	token, err := g.auth.issue()
	if err != nil {
		t.Fatal(err)
	}
	w := request(g, "POST", "/api/v1/auth/exchange", `{"token":"`+token+`"}`, g.host, "http://"+g.host, "", "")
	if w.Code != 200 {
		t.Fatalf("exchange %d %s", w.Code, w.Body.String())
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure || c.Path != "/" || c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Fatalf("cookie %#v", c)
	}
	var v struct {
		Data struct {
			CSRF string `json:"csrf"`
		}
	}
	json.Unmarshal(w.Body.Bytes(), &v)
	if v.Data.CSRF == "" {
		t.Fatal("missing csrf")
	}
	return c.Name + "=" + c.Value, v.Data.CSRF
}
func TestAuthExchangeExpiryAndReplay(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	now := time.Now()
	g.auth.now = func() time.Time { return now }
	token, _ := g.auth.issue()
	now = now.Add(60 * time.Second)
	if w := request(g, "POST", "/api/v1/auth/exchange", `{"token":"`+token+`"}`, g.host, "http://"+g.host, "", ""); w.Code != 401 {
		t.Fatalf("expiry %d", w.Code)
	}
	cookie, csrf := authenticated(t, g)
	if w := request(g, "GET", "/api/v1/auth/session", "", g.host, "", cookie, ""); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(csrf)) {
		t.Fatalf("refresh %d %s", w.Code, w.Body.String())
	}
	token, _ = g.auth.issue()
	body := `{"token":"` + token + `"}`
	if w := request(g, "POST", "/api/v1/auth/exchange", body, g.host, "http://"+g.host, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(g, "POST", "/api/v1/auth/exchange", body, g.host, "http://"+g.host, "", ""); w.Code != 401 {
		t.Fatalf("replay %d", w.Code)
	}
	now = now.Add(8 * time.Hour)
	if w := request(g, "GET", "/api/v1/auth/session", "", g.host, "", cookie, ""); w.Code != 401 {
		t.Fatalf("idle expiry %d", w.Code)
	}
}
func TestHostOriginAndCSRFRejected(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	cookie, csrf := authenticated(t, g)
	for _, v := range []struct{ host, origin, csrf string }{{"evil.test:16800", "http://evil.test:16800", csrf}, {g.host, "http://evil.test", csrf}, {g.host, "", csrf}, {g.host, "http://" + g.host, ""}} {
		if w := request(g, "POST", "/api/v1/sessions/x/plans", `{}`, v.host, v.origin, cookie, v.csrf); w.Code != 403 {
			t.Fatalf("%#v: %d", v, w.Code)
		}
	}
	for _, body := range []string{`{"token":"x","unknown":1}`, `{"token":"x"} {}`, string(bytes.Repeat([]byte("x"), 65537))} {
		if w := request(g, "POST", "/api/v1/auth/exchange", body, g.host, "http://"+g.host, "", ""); w.Code != 400 {
			t.Fatalf("invalid body %d", w.Code)
		}
	}
}
func TestSSERequiresAuth(t *testing.T) {
	hits := 0
	g := NewGateway("127.0.0.1:16800", Options{Events: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(200) })})
	if w := request(g, "GET", "/api/v1/events", "", g.host, "", "", ""); w.Code != 401 || hits != 0 {
		t.Fatalf("unauth SSE %d %d", w.Code, hits)
	}
	cookie, _ := authenticated(t, g)
	if w := request(g, "GET", "/api/v1/events", "", g.host, "", cookie, ""); w.Code != 200 || hits != 1 {
		t.Fatalf("auth SSE %d %d", w.Code, hits)
	}
}
func TestAuthCapacityAndCredentialInFragment(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	for i := 0; i < 128; i++ {
		if _, err := g.auth.issue(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.auth.issue(); err == nil {
		t.Fatal("credential cap missing")
	}
	g = NewGateway("127.0.0.1:16800", Options{})
	for i := 0; i < 32; i++ {
		authenticated(t, g)
	}
	token, _ := g.auth.issue()
	if w := request(g, "POST", "/api/v1/auth/exchange", `{"token":"`+token+`"}`, g.host, "http://"+g.host, "", ""); w.Code != 429 {
		t.Fatalf("session cap %d", w.Code)
	}
}

func TestAuthenticatedReadsExtendServerIdleExpiry(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	now := time.Now()
	g.auth.now = func() time.Time { return now }
	cookie, _ := authenticated(t, g)
	for i := 0; i < 2; i++ {
		now = now.Add(7 * time.Hour)
		w := request(g, "GET", "/api/v1/auth/session", "", g.host, "", cookie, "")
		if w.Code != http.StatusOK || len(w.Result().Cookies()) != 0 {
			t.Fatalf("active session %d: %d %#v", i, w.Code, w.Result().Cookies())
		}
		var body struct {
			Data struct {
				ExpiresAt time.Time `json:"expires_at"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !body.Data.ExpiresAt.Equal(now.Add(sessionIdle)) {
			t.Fatalf("rolling idle expiry %s", body.Data.ExpiresAt)
		}
	}
	// The original cookie remains usable after fourteen active hours; exactly
	// eight hours without another authenticated request expires server authority.
	now = now.Add(sessionIdle)
	if w := request(g, "GET", "/api/v1/auth/session", "", g.host, "", cookie, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("idle session remained valid %d", w.Code)
	}
}

func TestAuthenticatedResponsesDoNotSetCookie(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{
		Collect: func(context.Context) (inventory.Inventory, error) { return inventory.Inventory{}, nil },
		Events:  http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
	})
	cookie, csrf := authenticated(t, g)
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/auth/session", http.StatusOK},
		{"GET", "/api/v1/inventory", http.StatusOK},
		{"GET", "/api/v1/events", http.StatusOK},
		{"POST", "/api/v1/auth/session", http.StatusMethodNotAllowed},
	} {
		w := request(g, test.method, test.path, "", g.host, "http://"+g.host, cookie, csrf)
		if w.Code != test.status || len(w.Result().Cookies()) != 0 {
			t.Fatalf("%s %s: %d cookies=%#v", test.method, test.path, w.Code, w.Result().Cookies())
		}
	}
}

func TestAuthRestartInvalidatesAllCredentials(t *testing.T) {
	old := NewGateway("127.0.0.1:16800", Options{Namespace: "/same-cache"})
	cookie, _ := authenticated(t, old)
	token, _ := old.auth.issue()
	restarted := NewGateway(old.host, old.options)
	if restarted.auth.cookieName != old.auth.cookieName {
		t.Fatal("unstable cookie identity")
	}
	if w := request(restarted, "GET", "/api/v1/auth/session", "", restarted.host, "", cookie, ""); w.Code != 401 {
		t.Fatalf("old cookie survived %d", w.Code)
	}
	if w := request(restarted, "POST", "/api/v1/auth/exchange", `{"token":"`+token+`"}`, restarted.host, "http://"+restarted.host, "", ""); w.Code != 401 {
		t.Fatalf("old launch token survived %d", w.Code)
	}
}

func TestLaunchExchangeReplacesSameBrowserAtomically(t *testing.T) {
	g := NewGateway("127.0.0.1:16800", Options{})
	cookie, _ := authenticated(t, g)
	for i := 0; i < 40; i++ {
		token, _ := g.auth.issue()
		old := cookie
		w := request(g, "POST", "/api/v1/auth/exchange", `{"token":"`+token+`"}`, g.host, "http://"+g.host, cookie, "")
		if w.Code != 200 {
			t.Fatalf("reopen %d: %d %s", i, w.Code, w.Body.String())
		}
		c := w.Result().Cookies()[0]
		cookie = c.Name + "=" + c.Value
		if w := request(g, "GET", "/api/v1/auth/session", "", g.host, "", old, ""); w.Code != 401 {
			t.Fatal("old session survived replacement", w.Code)
		}
		if len(g.auth.sessions) != 1 {
			t.Fatal("reopen retained sessions", len(g.auth.sessions))
		}
	}
	token, _ := g.auth.issue()
	w := request(g, "POST", "/api/v1/auth/exchange", `{"token":"invalid"}`, g.host, "http://"+g.host, cookie, "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(g, "GET", "/api/v1/auth/session", "", g.host, "", cookie, ""); w.Code != 200 {
		t.Fatal("failed exchange destroyed valid session", w.Code)
	}
	_ = token
}

func TestLaunchExchangeInFlightResponsePreservesRotatedCookie(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	g := NewGateway("127.0.0.1:16800", Options{Collect: func(context.Context) (inventory.Inventory, error) {
		close(entered)
		<-release
		return inventory.Inventory{}, nil
	}})
	cookie, _ := authenticated(t, g)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse("http://" + g.host)
	initial, _ := http.NewRequest("GET", origin.String(), nil)
	initial.Header.Set("Cookie", cookie)
	jar.SetCookies(origin, initial.Cookies())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- request(g, "GET", "/api/v1/inventory", "", g.host, "", cookie, "")
	}()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("inventory did not reach the blocked collector")
	}
	token, err := g.auth.issue()
	if err != nil {
		t.Fatal(err)
	}
	exchange := request(g, "POST", "/api/v1/auth/exchange", `{"token":"`+token+`"}`, g.host, origin.String(), cookie, "")
	if exchange.Code != http.StatusOK {
		t.Fatalf("exchange %d %s", exchange.Code, exchange.Body.String())
	}
	rotated := exchange.Result().Cookies()
	jar.SetCookies(origin, rotated)
	close(release)
	released = true
	var earlier *httptest.ResponseRecorder
	select {
	case earlier = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("inventory did not finish after release")
	}
	if earlier.Code != http.StatusOK {
		t.Fatalf("earlier inventory %d %s", earlier.Code, earlier.Body.String())
	}
	// Apply responses in browser arrival order: exchange, then the older read.
	jar.SetCookies(origin, earlier.Result().Cookies())
	current := jar.Cookies(origin)
	if len(current) != 1 {
		t.Fatalf("browser cookies %#v", current)
	}
	w := request(g, "GET", "/api/v1/auth/session", "", g.host, "", current[0].String(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("late pre-rotation inventory response invalidated the browser: %d", w.Code)
	}
	if len(earlier.Result().Cookies()) != 0 || current[0].Value != rotated[0].Value {
		t.Fatal("ordinary response changed the rotated browser cookie")
	}
	if w := request(g, "GET", "/api/v1/auth/session", "", g.host, "", cookie, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("old session survived rotation %d", w.Code)
	}
}

func TestBrowserJSONContentType(t *testing.T) {
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded", "application/json; charset", "application/json; charset=utf-8; charset=ascii"} {
		t.Run(contentType, func(t *testing.T) {
			g := NewGateway("127.0.0.1:16800", Options{})
			token, err := g.auth.issue()
			if err != nil {
				t.Fatal(err)
			}
			body := `{"token":"` + token + `"}`
			r := httptest.NewRequest("POST", "http://"+g.host+"/api/v1/auth/exchange", bytes.NewBufferString(body))
			r.Header.Set("Origin", "http://"+g.host)
			r.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest || len(w.Result().Cookies()) != 0 || len(g.auth.sessions) != 0 {
				t.Fatalf("non-JSON media type accepted: %d %s", w.Code, w.Body.String())
			}
			// Rejection must not consume the one-use launch credential.
			if valid := request(g, "POST", "/api/v1/auth/exchange", body, g.host, "http://"+g.host, "", ""); valid.Code != http.StatusOK {
				t.Fatalf("rejected media type consumed token: %d", valid.Code)
			}
		})
	}
	for _, contentType := range []string{"application/json", "application/json; charset=utf-8"} {
		g := NewGateway("127.0.0.1:16800", Options{})
		token, _ := g.auth.issue()
		r := httptest.NewRequest("POST", "http://"+g.host+"/api/v1/auth/exchange", bytes.NewBufferString(`{"token":"`+token+`"}`))
		r.Header.Set("Origin", "http://"+g.host)
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("valid media type %q: %d %s", contentType, w.Code, w.Body.String())
		}
	}
}
