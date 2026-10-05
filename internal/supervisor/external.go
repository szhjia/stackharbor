package supervisor

import (
	"context"
	"net"
	"strconv"

	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/observe"
)

func externalDockerServices(specs map[model.ServiceID]model.Service, ports map[model.ServiceID][]model.PortObservation, owned map[model.ServiceID][]model.ProcessIdentity, rows []model.DockerSnapshot) map[model.ServiceID]model.DockerSnapshot {
	out := map[model.ServiceID]model.DockerSnapshot{}
	for id, spec := range specs {
		if len(owned[id]) > 0 || spec.Kind == "task" || spec.Kind == "resource" {
			continue
		}
		matches := map[string]model.DockerSnapshot{}
		for _, port := range ports[id] {
			if port.Status != "external" || len(port.Listeners) == 0 {
				continue
			}
			forwarder := true
			for _, listener := range port.Listeners {
				forwarder = forwarder && observe.IsForwarder(listener.Command)
			}
			if !forwarder {
				continue
			}
			for _, row := range rows {
				if row.State != "running" || row.ID == "" {
					continue
				}
				for _, published := range row.PublishedEndpoints {
					if published.Port != port.Port {
						continue
					}
					for _, listener := range port.Listeners {
						if publishedListenerMatches(published, listener) {
							matches[row.ID] = row
						}
					}
				}
			}
		}
		if len(matches) == 1 {
			for _, row := range matches {
				out[id] = row
			}
		}
	}
	return out
}

func observeExternalServices(ctx context.Context, specs map[model.ServiceID]model.Service, ports map[model.ServiceID][]model.PortObservation, owned map[model.ServiceID][]model.ProcessIdentity) (map[model.ServiceID]string, map[model.ServiceID]string) {
	states, reasons := map[model.ServiceID]string{}, map[model.ServiceID]string{}
	for id, spec := range specs {
		if len(owned[id]) > 0 || spec.Kind == "task" || spec.Kind == "resource" {
			continue
		}
		external, unknown := false, false
		for _, port := range ports[id] {
			external = external || port.Status == "external"
			unknown = unknown || port.Status == "unknown"
		}
		if unknown {
			states[id] = "unknown"
			continue
		}
		if !external {
			continue
		}
		states[id] = "listening"
		if spec.Ready != nil {
			if err := observe.CheckHealth(ctx, *spec.Ready); err != nil {
				states[id], reasons[id] = "unready", "External readiness: "+err.Error()
			} else {
				states[id] = "running"
			}
		}
	}
	return states, reasons
}

func publishedListenerMatches(endpoint model.PublishedEndpoint, listener model.Listener) bool {
	host, port, err := net.SplitHostPort(listener.Address)
	if err != nil || port != strconv.Itoa(endpoint.Port) {
		return false
	}
	published := net.ParseIP(endpoint.Host)
	address := net.ParseIP(host)
	if endpoint.Host == "localhost" {
		published = net.ParseIP("127.0.0.1")
	}
	if host == "localhost" {
		address = net.ParseIP("127.0.0.1")
	}
	if endpoint.Host == "" || host == "*" {
		return false
	}
	if published == nil || address == nil {
		return false
	}
	if published.IsUnspecified() || address.IsUnspecified() {
		return (published.To4() == nil) == (address.To4() == nil)
	}
	return published.Equal(address)
}
