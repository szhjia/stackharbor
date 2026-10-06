package sessionapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

type Registration struct {
	Info            supervisor.SessionInfo
	PublishEndpoint func(supervisor.Endpoint) error
}

// Response is the shared JSON envelope. Errors always carry a stable APIError code.
type Response[T any] struct {
	Data  T                 `json:"data,omitempty"`
	Error *control.APIError `json:"error,omitempty"`
}
type Handshake struct {
	control.Identity
	Root                 string `json:"root"`
	NamespaceID          string `json:"namespace_id"`
	PID                  int    `json:"pid"`
	ProcessCreatedMillis int64  `json:"process_created_millis"`
}
type Server struct {
	http       *http.Server
	listener   *net.UnixListener
	path       string
	inode      os.FileInfo
	controller *control.Coordinator
	handshake  Handshake
	once       sync.Once
	done       chan struct{}
	closeErr   error
}

func Listen(ctx context.Context, registration Registration, controller *control.Coordinator) (*Server, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if controller == nil || registration.PublishEndpoint == nil {
		return nil, &control.APIError{Code: "invalid_request", Message: "controller and endpoint publisher required"}
	}
	info := registration.Info
	id := controller.Identity()
	if id.ProtocolVersion != ProtocolVersion {
		return nil, &control.APIError{Code: "unsupported_protocol", Message: "unsupported session protocol"}
	}
	if id.WorkspaceID == "" || id.WorkspaceID != info.WorkspaceID || id.SessionID == "" || id.SessionID != info.SessionID || info.PID != os.Getpid() || info.ProcessCreatedMillis <= 0 {
		return nil, identityConflict()
	}
	if err := verifyProcess(ctx, info); err != nil {
		return nil, err
	}
	path, err := socketPath(info)
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, unavailable(err)
	}
	listener.SetUnlinkOnClose(false)
	inode, err := os.Lstat(path)
	if err != nil {
		listener.Close()
		return nil, err
	}
	cleanup := func() {
		listener.Close()
		if samePath(inode, path) {
			os.Remove(path)
		}
	}
	if err = os.Chmod(path, 0600); err != nil {
		cleanup()
		return nil, err
	}
	if _, err = validateSocket(path); err != nil {
		cleanup()
		return nil, err
	}
	s := &Server{listener: listener, path: path, inode: inode, controller: controller, done: make(chan struct{}), handshake: Handshake{Identity: id, Root: info.Root, NamespaceID: info.NamespaceID, PID: info.PID, ProcessCreatedMillis: info.ProcessCreatedMillis}}
	s.http = &http.Server{Handler: http.TimeoutHandler(http.HandlerFunc(s.serve), 10*time.Second, `{"error":{"code":"unavailable","message":"request timed out"}}`), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	go func() { defer close(s.done); s.http.Serve(listener) }()
	if err = registration.PublishEndpoint(supervisor.Endpoint{SocketPath: path, ProtocolVersion: ProtocolVersion, Capabilities: id.Capabilities}); err != nil {
		s.Close(context.Background())
		return nil, err
	}
	return s, nil
}

// Close tears down only the transport. It never shuts down the coordinator or supervisor.
func (s *Server) Close(ctx context.Context) error {
	s.once.Do(func() {
		s.closeErr = s.http.Shutdown(ctx)
		if s.closeErr != nil {
			s.http.Close()
		}
		if samePath(s.inode, s.path) {
			if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) && s.closeErr == nil {
				s.closeErr = err
			}
		}
	})
	return s.closeErr
}
func StatusCode(err error) int {
	var api *control.APIError
	if !errors.As(err, &api) {
		return http.StatusServiceUnavailable
	}
	switch api.Code {
	case "invalid_request":
		return 400
	case "unauthenticated":
		return 401
	case "forbidden":
		return 403
	case "not_found":
		return 404
	case "queue_full", "plan_capacity":
		return 429
	case "unsupported_protocol":
		return 409
	case "session_closed":
		return 409
	}
	if strings.HasSuffix(api.Code, "_expired") {
		return 410
	}
	if strings.HasSuffix(api.Code, "_conflict") {
		return 409
	}
	return 503
}
func writeJSON(w http.ResponseWriter, status int, data any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err != nil {
		var api *control.APIError
		if !errors.As(err, &api) {
			api = &control.APIError{Code: "unavailable", Message: err.Error()}
		}
		json.NewEncoder(w).Encode(Response[any]{Error: api})
		return
	}
	json.NewEncoder(w).Encode(Response[any]{Data: data})
}
func failure(w http.ResponseWriter, err error) { writeJSON(w, StatusCode(err), nil, err) }
func invalid(message string) *control.APIError {
	return &control.APIError{Code: "invalid_request", Message: message}
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return invalid("invalid JSON request: " + err.Error())
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return invalid("request must contain exactly one JSON value")
	}
	return nil
}
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	if r.URL.Path == "/v1/operations" {
		method = "GET, POST"
	}
	w.Header().Set("Allow", method)
	writeJSON(w, 405, nil, invalid("unsupported HTTP method"))
	return false
}
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/identity":
		if requireMethod(w, r, "GET") {
			writeJSON(w, 200, s.handshake, nil)
		}
	case "/v1/snapshot":
		if !requireMethod(w, r, "GET") {
			return
		}
		v, err := s.controller.Snapshot(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		writeJSON(w, 200, v, nil)
	case "/v1/plans":
		if !requireMethod(w, r, "POST") {
			return
		}
		var req control.PlanRequest
		if err := decode(w, r, &req); err != nil {
			failure(w, err)
			return
		}
		v, err := s.controller.Plan(r.Context(), req)
		if err != nil {
			failure(w, err)
			return
		}
		writeJSON(w, 200, v, nil)
	case "/v1/operations":
		if r.Method == "GET" {
			writeJSON(w, 200, s.controller.Operations(), nil)
			return
		}
		if !requireMethod(w, r, "POST") {
			return
		}
		var req control.SubmitRequest
		if err := decode(w, r, &req); err != nil {
			failure(w, err)
			return
		}
		v, err := s.controller.Submit(r.Context(), req)
		if err != nil {
			failure(w, err)
			return
		}
		writeJSON(w, 202, v, nil)
	case "/v1/logs":
		if !requireMethod(w, r, "GET") {
			return
		}
		q, parseErr := url.ParseQuery(r.URL.RawQuery)
		if parseErr != nil {
			failure(w, invalid("invalid logs query encoding"))
			return
		}
		for key, values := range q {
			if (key != "target" && key != "after" && key != "limit") || len(values) != 1 {
				failure(w, invalid("invalid logs query"))
				return
			}
		}
		after := uint64(0)
		limit := 500
		var err error
		if value, ok := q["after"]; ok {
			after, err = strconv.ParseUint(value[0], 10, 64)
			if err != nil {
				failure(w, invalid("after must be an unsigned sequence"))
				return
			}
		}
		if value, ok := q["limit"]; ok {
			limit, err = strconv.Atoi(value[0])
			if err != nil || limit < 1 || limit > 500 {
				failure(w, invalid("limit must be between 1 and 500"))
				return
			}
		}
		v, err := s.controller.Logs(r.Context(), q.Get("target"), after, limit)
		if err != nil {
			failure(w, err)
			return
		}
		writeJSON(w, 200, v, nil)
	default:
		rest := strings.TrimPrefix(r.URL.Path, "/v1/operations/")
		if rest == r.URL.Path || rest == "" {
			failure(w, &control.APIError{Code: "not_found", Message: "route not found"})
			return
		}
		if strings.HasSuffix(rest, "/cancel") {
			id := strings.TrimSuffix(rest, "/cancel")
			if id == "" || strings.Contains(id, "/") {
				failure(w, &control.APIError{Code: "not_found", Message: "route not found"})
				return
			}
			if !requireMethod(w, r, "POST") {
				return
			}
			var req struct{}
			if err := decode(w, r, &req); err != nil {
				failure(w, err)
				return
			}
			if err := s.controller.Cancel(id); err != nil {
				failure(w, err)
				return
			}
			writeJSON(w, 200, struct{}{}, nil)
			return
		}
		if strings.Contains(rest, "/") {
			failure(w, &control.APIError{Code: "not_found", Message: "route not found"})
			return
		}
		if !requireMethod(w, r, "GET") {
			return
		}
		v, err := s.controller.Operation(rest)
		if err != nil {
			failure(w, err)
			return
		}
		writeJSON(w, 200, v, nil)
	}
}
