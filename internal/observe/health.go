package observe

import (
	"context"
	"fmt"
	"github.com/szhjia/stackharbor/internal/config"
	"github.com/szhjia/stackharbor/internal/model"
	"net"
	"net/http"
	"net/url"
	"time"
)

func CheckHealth(ctx context.Context, p model.ReadyProbe) error {
	if e := config.ProbeTarget(p); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		if !config.LoopbackHost(host) {
			return nil, fmt.Errorf("non-loopback target")
		}
		if host == "localhost" {
			host = "127.0.0.1"
		}
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(host, port))
	}
	if p.TCP != "" {
		conn, e := dial(ctx, "tcp", p.TCP)
		if e != nil {
			return e
		}
		return conn.Close()
	}
	transport := &http.Transport{DialContext: dial}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("redirect limit")
		}
		return config.ProbeTarget(model.ReadyProbe{HTTP: req.URL.String()})
	}}
	u, e := url.Parse(p.HTTP)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return e
	}
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP readiness returned %d", resp.StatusCode)
	}
	return nil
}
