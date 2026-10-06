package sessionapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/supervisor"
)

type Client struct {
	http      *http.Client
	transport *http.Transport
	handshake Handshake
}

func Connect(ctx context.Context, info supervisor.SessionInfo) (*Client, error) {
	if info.ProtocolVersion != ProtocolVersion {
		return nil, &control.APIError{Code: "unsupported_protocol", Message: "session supports metadata discovery only; control protocol is unavailable or incompatible"}
	}
	if info.PID <= 0 || info.ProcessCreatedMillis <= 0 || info.WorkspaceID == "" || info.SessionID == "" || info.NamespaceID == "" {
		return nil, identityConflict()
	}
	if err := verifyProcess(ctx, info); err != nil {
		return nil, err
	}
	inode, err := validateSocket(info.SocketPath)
	if err != nil {
		return nil, unavailable(err)
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		// Recheck every new connection so cached metadata cannot authorize a replacement.
		if !samePath(inode, info.SocketPath) {
			return nil, identityConflict()
		}
		if _, err := validateSocket(info.SocketPath); err != nil {
			return nil, err
		}
		conn, err := dial(ctx, info.SocketPath)
		if err != nil {
			return nil, err
		}
		if !samePath(inode, info.SocketPath) {
			conn.Close()
			return nil, identityConflict()
		}
		return conn, nil
	}, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConnsPerHost: 2}
	c := &Client{http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport: transport}
	var handshake Handshake
	if err = c.request(ctx, "GET", "/v1/identity", nil, &handshake); err != nil {
		c.Close()
		return nil, err
	}
	if handshake.ProtocolVersion != ProtocolVersion {
		c.Close()
		return nil, &control.APIError{Code: "unsupported_protocol", Message: "endpoint has an incompatible protocol"}
	}
	if handshake.WorkspaceID != info.WorkspaceID || handshake.SessionID != info.SessionID || handshake.PID != info.PID || handshake.ProcessCreatedMillis != info.ProcessCreatedMillis || handshake.Root != info.Root || handshake.NamespaceID != info.NamespaceID {
		c.Close()
		return nil, identityConflict()
	}
	c.handshake = handshake
	return c, nil
}
func (c *Client) Close() error { c.transport.CloseIdleConnections(); return nil }
func (c *Client) Identity() control.Identity {
	id := c.handshake.Identity
	id.Capabilities = append([]string{}, id.Capabilities...)
	return id
}
func (c *Client) raw(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://session"+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}
func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	response, err := c.raw(ctx, method, path, body)
	if err != nil {
		return unavailable(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Data  json.RawMessage   `json:"data"`
		Error *control.APIError `json:"error"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8*1024*1024))
	if err = decoder.Decode(&envelope); err != nil {
		return unavailable(err)
	}
	if envelope.Error != nil {
		return envelope.Error
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &control.APIError{Code: "unavailable", Message: "unexpected session HTTP response"}
	}
	if output != nil {
		if err = json.Unmarshal(envelope.Data, output); err != nil {
			return unavailable(err)
		}
	}
	return nil
}
func (c *Client) Snapshot(ctx context.Context) (v control.Snapshot, err error) {
	err = c.request(ctx, "GET", "/v1/snapshot", nil, &v)
	return
}
func (c *Client) Plan(ctx context.Context, req control.PlanRequest) (v control.Plan, err error) {
	err = c.request(ctx, "POST", "/v1/plans", req, &v)
	return
}
func (c *Client) Submit(ctx context.Context, req control.SubmitRequest) (v control.Operation, err error) {
	err = c.request(ctx, "POST", "/v1/operations", req, &v)
	return
}
func (c *Client) Operation(ctx context.Context, id string) (v control.Operation, err error) {
	err = c.request(ctx, "GET", "/v1/operations/"+url.PathEscape(id), nil, &v)
	return
}
func (c *Client) Operations(ctx context.Context) (v []control.Operation, err error) {
	err = c.request(ctx, "GET", "/v1/operations", nil, &v)
	return
}
func (c *Client) Cancel(ctx context.Context, id string) error {
	return c.request(ctx, "POST", "/v1/operations/"+url.PathEscape(id)+"/cancel", struct{}{}, nil)
}
func (c *Client) Logs(ctx context.Context, target string, after uint64, limit int) (v control.LogPage, err error) {
	q := url.Values{"target": {target}, "after": {strconv.FormatUint(after, 10)}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	err = c.request(ctx, "GET", "/v1/logs?"+q.Encode(), nil, &v)
	return
}

// LogsAfter validates a session/target-bound cursor before translating it to
// the independent Session API's numeric sequence protocol.
func (c *Client) LogsAfter(ctx context.Context, target, cursor string, limit int) (control.BoundLogPage, error) {
	after, reset, err := control.DecodeLogCursor(cursor, c.handshake.SessionID, target)
	if err != nil {
		return control.BoundLogPage{}, err
	}
	page, err := c.Logs(ctx, target, after, limit)
	if err != nil {
		return control.BoundLogPage{}, err
	}
	page.Reset = page.Reset || reset
	return control.BoundLogPage{LogPage: page, SessionID: c.handshake.SessionID, Target: target, Cursor: control.EncodeLogCursor(c.handshake.SessionID, target, page.NextCursor)}, nil
}
