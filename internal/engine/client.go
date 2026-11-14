package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/process"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/supervisor"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Endpoint struct {
	SocketPath           string `json:"socket_path"`
	PID                  int    `json:"pid"`
	ProcessCreatedMillis int64  `json:"process_created_millis"`
	ProtocolVersion      int    `json:"protocol_version"`
	BuildVersion         string `json:"build_version"`
}
type OpenRequest struct {
	Root        string   `json:"root"`
	Namespace   string   `json:"namespace,omitempty"`
	Workspace   string   `json:"workspace,omitempty"`
	Environment []string `json:"environment,omitempty"`
	Shell       bool     `json:"shell,omitempty"`
}
type Client struct {
	Endpoint Endpoint
	http     *http.Client
	inode    os.FileInfo
}

// Connect never launches the engine; status and shutdown must be read-only when
// nothing is running.
func Connect(ctx context.Context) (*Client, error) {
	dir, err := runtimePath()
	if err != nil {
		return nil, err
	}
	if err = sessionapi.ValidatePrivateDirectory(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "engine.json")
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("unsafe engine endpoint metadata")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e Endpoint
	if len(b) > 16384 || json.Unmarshal(b, &e) != nil {
		return nil, fmt.Errorf("invalid engine endpoint")
	}
	if e.ProtocolVersion != ProtocolVersion {
		return nil, &control.APIError{Code: "unsupported_protocol", Message: "Running engine protocol differs; use its cached old CLI before upgrading"}
	}
	if e.SocketPath != filepath.Join(dir, "engine.sock") || e.PID <= 0 || e.ProcessCreatedMillis <= 0 {
		return nil, fmt.Errorf("engine endpoint identity changed")
	}
	inode, err := sessionapi.ValidatePrivateSocket(e.SocketPath)
	if err != nil {
		return nil, err
	}
	birth, err := process.CreatedMillis(ctx, int32(e.PID))
	if err != nil || birth != e.ProcessCreatedMillis {
		return nil, fmt.Errorf("engine process identity changed")
	}
	c := &Client{Endpoint: e, inode: inode}
	c.http = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		before, err := sessionapi.ValidatePrivateSocket(e.SocketPath)
		if err != nil || !os.SameFile(before, inode) {
			return nil, fmt.Errorf("engine socket identity changed")
		}
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", e.SocketPath)
		if err != nil {
			return nil, err
		}
		after, err := sessionapi.ValidatePrivateSocket(e.SocketPath)
		if err != nil || !os.SameFile(after, inode) {
			conn.Close()
			return nil, fmt.Errorf("engine socket replaced")
		}
		return conn, nil
	}}, Timeout: 11 * time.Second}
	var actual Endpoint
	if err = c.Do(ctx, "GET", "/engine/v1/identity", nil, &actual); err != nil {
		c.Close()
		return nil, err
	}
	if actual.PID != e.PID || actual.ProcessCreatedMillis != e.ProcessCreatedMillis || actual.SocketPath != e.SocketPath || actual.ProtocolVersion != e.ProtocolVersion {
		c.Close()
		return nil, fmt.Errorf("engine handshake identity changed")
	}
	return c, nil
}
func (c *Client) Close() error {
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
	return nil
}
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	if !strings.HasPrefix(path, "/engine/v1/") && !strings.HasPrefix(path, "/api/v1/") {
		return fmt.Errorf("invalid engine route")
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://engine"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if err != nil {
		return err
	}
	if len(raw) > 8*1024*1024 {
		return fmt.Errorf("engine response exceeds limit")
	}
	var envelope struct {
		Data  json.RawMessage   `json:"data"`
		Error *control.APIError `json:"error"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if envelope.Error != nil {
		return envelope.Error
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("engine returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(envelope.Data, out)
	}
	return nil
}
func (c *Client) Open(ctx context.Context, r OpenRequest) (supervisor.SessionInfo, error) {
	var out supervisor.SessionInfo
	err := c.Do(ctx, "POST", "/engine/v1/open", r, &out)
	return out, err
}
func Ensure(ctx context.Context) (*Client, error) {
	if c, err := Connect(ctx); err == nil {
		if c.Endpoint.BuildVersion != buildinfo.Version {
			c.Close()
			return nil, &control.APIError{Code: "version_conflict", Message: "Running engine uses another version; stop projects with its cached CLI before upgrading"}
		}
		return c, nil
	} else {
		var api *control.APIError
		if errors.As(err, &api) {
			return nil, err
		}
	}
	if os.Getenv("STACKHARBOR_ENGINE_RUNTIME_DIR") != "" || os.Getenv("STACKHARBOR_ENGINE_STATE_DIR") != "" {
		return nil, fmt.Errorf("isolated engine is not running; start engine serve with the same explicit directories")
	}
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("engine is not running; start stackharbor engine serve for development")
	}
	budget, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(budget, "/bin/launchctl", "kickstart", fmt.Sprintf("gui/%d/%s", os.Getuid(), AgentLabel))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("background engine is unavailable; open StackHarbor and complete setup or enable its background item in System Settings")
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if c, err := Connect(budget); err == nil {
			if c.Endpoint.BuildVersion != buildinfo.Version {
				c.Close()
				return nil, &control.APIError{Code: "version_conflict", Message: "Running engine uses another version; stop projects with its cached CLI before upgrading"}
			}
			return c, nil
		}
		select {
		case <-budget.Done():
			return nil, fmt.Errorf("engine startup timed out")
		case <-ticker.C:
		}
	}
}
