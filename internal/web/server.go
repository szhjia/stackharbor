// Package web is a loopback-only gateway to independently owned session APIs.
package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/inventory"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

// Options.Namespace is the registry cache directory, defaulting to
// STACKHARBOR_CACHE_DIR then the user's StackHarbor cache. Static and Events
// are optional injections; Static defaults to the real embedded UI. Events defaults to the live event hub.
type Options struct {
	DevelopmentOrigin string
	Port              int
	PortExplicit      bool
	NoOpen            bool
	Namespace         string
	OpenURL           func(string) error
	Out, ErrOut       io.Writer
	Ready             func(string)
	Static, Events    http.Handler
	Collect           func(context.Context) (inventory.Inventory, error)
}
type retainedPlan struct {
	Plan        control.Plan
	Info        supervisor.SessionInfo
	AcceptedKey string
	Operation   *control.Operation
}
type Gateway struct {
	host      string
	options   Options
	auth      *authStore
	collector *inventory.Collector
	mu        sync.Mutex
	plans     map[string]retainedPlan
	closes    map[string]retainedClose
	liveMu    sync.RWMutex
	live      *liveInventory
	hub       *EventHub
}

func NewGateway(host string, options Options) *Gateway {
	if options.Static == nil {
		options.Static = Assets()
	}
	digest := sha256.Sum256([]byte(host + "|" + options.Namespace))
	return &Gateway{host: host, options: options, auth: newAuth(cookiePrefix + fmt.Sprintf("%x", digest[:12])), collector: inventory.New(registrySource{options.Namespace}, inventory.Options{}), plans: map[string]retainedPlan{}, closes: map[string]retainedClose{}, hub: NewEventHub()}
}

type registrySource struct{ cache string }

func (s registrySource) ListSessions(ctx context.Context) ([]supervisor.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.cache == "" {
		return supervisor.ListSessions()
	}
	return supervisor.ListSessionsIn(s.cache)
}
func (registrySource) Connect(ctx context.Context, info supervisor.SessionInfo) (inventory.SnapshotClient, error) {
	return sessionapi.Connect(ctx, info)
}
func (g *Gateway) collect(ctx context.Context) (inventory.Inventory, error) {
	if g.options.Collect != nil {
		return g.options.Collect(ctx)
	}
	return g.collector.Collect(ctx)
}
func writeJSON(w http.ResponseWriter, status int, data any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err != nil {
		var api *control.APIError
		if !errors.As(err, &api) {
			api = apiError("unavailable", err.Error())
		}
		json.NewEncoder(w).Encode(sessionapi.Response[any]{Error: api})
		return
	}
	json.NewEncoder(w).Encode(sessionapi.Response[any]{Data: data})
}
func fail(w http.ResponseWriter, err error) { writeJSON(w, sessionapi.StatusCode(err), nil, err) }
func decodeBrowserJSON(w http.ResponseWriter, r *http.Request, v any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return apiError("invalid_request", "Content-Type must be application/json")
	}
	return decode(w, r, v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return apiError("invalid_request", "invalid JSON request")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return apiError("invalid_request", "request must contain exactly one JSON value")
	}
	return nil
}
func method(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for _, v := range allowed {
		if r.Method == v {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeJSON(w, 405, nil, apiError("invalid_request", "unsupported HTTP method"))
	return false
}
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/events" {
		controller := http.NewResponseController(w)
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer controller.SetWriteDeadline(time.Time{})
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'")
	if r.Host != g.host {
		fail(w, apiError("forbidden", "unexpected Host header"))
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, apiError("forbidden", "cross-site request rejected"))
		return
	}
	origin := r.Header.Get("Origin")
	allowedOrigin := origin == "http://"+g.host || validDevelopmentOrigin(g.options.DevelopmentOrigin) && origin == g.options.DevelopmentOrigin
	if origin != "" && !allowedOrigin {
		fail(w, apiError("forbidden", "unexpected Origin header"))
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && !allowedOrigin {
		fail(w, apiError("forbidden", "same-origin request required"))
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if !method(w, r, "GET", "HEAD") {
			return
		}
		if g.options.Static == nil {
			fail(w, apiError("unavailable", "Web UI assets unavailable in this build"))
			return
		}
		g.options.Static.ServeHTTP(w, r)
		return
	}
	// Local browsers establish their request-protection session automatically.
	// Host, Origin and Fetch Metadata checks above still apply to this endpoint.
	if r.URL.Path == "/api/v1/auth/session" {
		if !method(w, r, "GET") {
			return
		}
		session, err := g.auth.authenticate(r, false)
		if err != nil {
			token, issueErr := g.auth.issue()
			if issueErr != nil {
				fail(w, issueErr)
				return
			}
			cookie, fresh, exchangeErr := g.auth.exchange(token)
			if exchangeErr != nil {
				fail(w, exchangeErr)
				return
			}
			g.setSessionCookie(w, cookie)
			session = fresh
		}
		writeJSON(w, 200, authDTO(session), nil)
		return
	}
	if r.URL.Path == "/api/v1/auth/exchange" {
		if !method(w, r, "POST") {
			return
		}
		var req struct {
			Token string `json:"token"`
		}
		if err := decodeBrowserJSON(w, r, &req); err != nil {
			fail(w, err)
			return
		}
		prior := ""
		if c, err := r.Cookie(g.auth.cookieName); err == nil {
			prior = c.Value
		}
		cookie, session, err := g.auth.exchange(req.Token, prior)
		if err != nil {
			fail(w, err)
			return
		}
		g.setSessionCookie(w, cookie)
		writeJSON(w, 200, authDTO(session), nil)
		return
	}
	_, err := g.auth.authenticate(r, r.Method != "GET" && r.Method != "HEAD")
	if err != nil {
		fail(w, err)
		return
	}
	// SSE owns its stream lifetime/write deadlines; ordinary requests get 10s.
	if r.URL.Path == "/api/v1/events" {
		if !method(w, r, "GET") {
			return
		}
		if g.options.Events != nil {
			g.options.Events.ServeHTTP(w, r)
		} else {
			g.serveEvents(w, r)
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	switch r.URL.Path {
	case "/api/v1/inventory":
		if method(w, r, "GET") {
			cursor := g.hub.Cursor()
			v, err := g.presentation(ctx)
			if err != nil {
				fail(w, err)
				return
			}
			writeJSON(w, 200, struct {
				inventory.Inventory
				EventCursor string `json:"event_cursor"`
			}{v, cursor}, nil)
		}
	default:
		g.forward(w, r)
	}
}

// Only session bootstrap or legacy launch exchange writes this cookie. Ordinary responses must
// not overwrite a credential rotated while an earlier request was in flight.
func (g *Gateway) setSessionCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{Name: g.auth.cookieName, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
func authDTO(s browserSession) any {
	return struct {
		CSRF      string    `json:"csrf"`
		ExpiresAt time.Time `json:"expires_at"`
	}{s.CSRF, s.LastSeen.Add(sessionIdle).UTC()}
}
