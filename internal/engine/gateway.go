package engine

import (
	"encoding/json"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/discovery"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"github.com/szhjia/stackharbor/internal/web"
	"net/http"
	"strings"
	"time"
)

func (s *Server) gateway(namespace string) *web.Gateway {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.gateways[namespace]; g != nil {
		return g
	}
	g := web.NewGateway("engine", web.Options{Namespace: namespace, Source: localSource{s, namespace}, LiveInterval: s.cadence,
		WorkspaceHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.workspaceAPI(namespace, w, r) }),
		OnSubscribe:      func() func() { s.foreground.Add(1); s.wakeObservation(); return func() { s.foreground.Add(-1) } },
	})
	s.gateways[namespace] = g
	g.Start(s.ctx)
	return g
}

func (s *Server) workspaceAPI(namespace string, w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/api/v1/workspaces" {
		engineJSON(w, 200, map[string]any{"workspaces": s.catalogue(namespace)}, nil)
		return
	}
	if r.Method != "POST" {
		engineJSON(w, 405, nil, &control.APIError{Code: "invalid_request", Message: "POST required"})
		return
	}
	var body struct {
		Root      string `json:"root"`
		Workspace string `json:"workspace,omitempty"`
	}
	if err := decodeRequest(w, r, &body); err != nil {
		engineJSON(w, 400, nil, err)
		return
	}
	request, err := s.normalize(OpenRequest{Root: body.Root, Workspace: body.Workspace, Namespace: namespace, Shell: true})
	if err != nil {
		engineJSON(w, 400, nil, err)
		return
	}
	var result any
	switch r.URL.Path {
	case "/api/v1/workspaces":
		// Resolve the same GUI toolchain for registration/Compose validation.
		// No registered project command is started by adding a root.
		environment, environmentErr := ShellEnvironment(r.Context(), request.Root)
		if environmentErr != nil {
			err = environmentErr
			break
		}
		ws := discovery.Discover(r.Context(), discovery.Options{Root: request.Root, RootExplicit: true, WorkspaceFile: request.Workspace, Environment: environment})
		if ws.Invalid() || len(ws.Services()) == 0 {
			err = &control.APIError{Code: "invalid_request", Message: "Choose a workspace with valid StackHarbor registration"}
			break
		}
		key := workspaceKey(namespace, request.Root)
		s.mu.Lock()
		s.state.mu.Lock()
		if e := s.state.entries[key]; e == nil {
			s.state.entries[key] = &workspaceState{Record: WorkspaceRecord{Root: request.Root, Namespace: namespace, Workspace: request.Workspace, State: "offline", UpdatedAt: time.Now().UTC()}, Attempts: map[string]attempt{}}
		}
		err = s.state.saveLocked()
		s.state.mu.Unlock()
		s.mu.Unlock()
		if err == nil {
			result = map[string]any{"workspaces": s.catalogue(namespace)}
		}
	case "/api/v1/workspaces/open":
		var info any
		info, err = s.Open(r.Context(), request)
		if err == nil {
			raw, _ := json.Marshal(info)
			var safe map[string]any
			_ = json.Unmarshal(raw, &safe)
			delete(safe, "socket_path")
			delete(safe, "cache_dir")
			result = safe
		}
	case "/api/v1/workspaces/reload":
		var info supervisor.SessionInfo
		info, err = s.reload(r.Context(), request)
		if err == nil {
			info.SocketPath = ""
			info.CacheDir = ""
			result = info
		}
	default:
		err = &control.APIError{Code: "not_found", Message: "Workspace route not found"}
	}
	if err != nil {
		engineJSON(w, sessionapi.StatusCode(err), nil, err)
	} else {
		engineJSON(w, 200, result, nil)
	}
}

// WorkspaceProxy lets the explicit browser gateway use exactly the engine's
// catalogue/open implementation while keeping its existing browser protection.
func (c *Client) WorkspaceProxy(namespace string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/workspaces") {
			http.NotFound(w, r)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), r.Method, "http://engine"+r.URL.RequestURI(), r.Body)
		if err != nil {
			engineJSON(w, 400, nil, err)
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-StackHarbor-Namespace", namespace)
		response, err := c.http.Do(request)
		if err != nil {
			engineJSON(w, 503, nil, fmt.Errorf("engine workspace API unavailable"))
			return
		}
		defer response.Body.Close()
		var data json.RawMessage
		if json.NewDecoder(http.MaxBytesReader(w, response.Body, 1<<20)).Decode(&data) != nil {
			engineJSON(w, 503, nil, fmt.Errorf("invalid engine response"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(data)
	})
}
