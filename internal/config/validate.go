package config

import (
	"fmt"
	"github.com/szhjia/stackharbor/internal/model"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func LoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func ProbeTarget(p model.ReadyProbe) error {
	if (p.HTTP == "") == (p.TCP == "") {
		return fmt.Errorf("ready requires exactly one of http or tcp")
	}
	host := ""
	if p.HTTP != "" {
		u, e := url.Parse(p.HTTP)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return fmt.Errorf("invalid HTTP readiness URL")
		}
		host = u.Hostname()
	} else {
		h, _, e := net.SplitHostPort(p.TCP)
		if e != nil {
			return e
		}
		host = h
	}
	if !LoopbackHost(host) {
		return fmt.Errorf("readiness target must be loopback")
	}
	return nil
}
func validateProject(path, root string, f projectFile) (model.Project, []model.Diagnostic) {
	p := model.Project{ID: f.Project.ID, Name: f.Project.Name, SourceFile: path}
	ds := []model.Diagnostic{}
	bad := func(field, message string) { ds = append(ds, model.Error(path, field, message)) }
	if f.Version != 1 {
		bad("version", "version must be 1")
	}
	if !identifier.MatchString(p.ID) {
		bad("project.id", "invalid project ID")
	}
	if strings.TrimSpace(p.Name) == "" {
		bad("project.name", "name is required")
	}
	if len(f.Services) == 0 || len(f.Services) > 1000 {
		bad("services", "requires 1–1000 services")
	}
	keys := []string{}
	for k := range f.Services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := f.Services[k]
		field := "services." + k
		if !identifier.MatchString(k) {
			bad(field, "invalid service key")
		}
		cwd, e := ResolveCwd(root, path, v.Cwd)
		if e != nil {
			bad(field+".cwd", e.Error())
		}
		if len(v.Run.Command) == 0 || strings.TrimSpace(v.Run.Command[0]) == "" {
			bad(field+".run.command", "command must not be empty")
		}
		for key, val := range v.Env {
			if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(val, 0) {
				bad(field+".env", "invalid environment entry")
			}
		}
		for _, arg := range v.Run.Command {
			if strings.ContainsRune(arg, 0) {
				bad(field+".run.command", "NUL in command")
			}
		}
		names := map[string]bool{}
		for _, port := range v.Ports {
			if port.Number < 1 || port.Number > 65535 || port.Name == "" || names[port.Name] {
				bad(field+".ports", "invalid or duplicate port name/number")
			}
			names[port.Name] = true
		}
		if v.Ready != nil {
			if e := ProbeTarget(*v.Ready); e != nil {
				bad(field+".ready", e.Error())
			}
			if v.Ready.TimeoutSeconds == 0 {
				v.Ready.TimeoutSeconds = 60
			}
			if v.Ready.TimeoutSeconds < 1 || v.Ready.TimeoutSeconds > 600 {
				bad(field+".ready.timeout_seconds", "must be 1–600")
			}
			v.Ready.Timeout = time.Duration(v.Ready.TimeoutSeconds) * time.Second
		}
		if v.Stop.Signal == "" {
			v.Stop.Signal = "TERM"
		}
		if v.Stop.Signal != "INT" && v.Stop.Signal != "TERM" {
			bad(field+".stop.signal", "must be INT or TERM")
		}
		if v.Stop.TimeoutSeconds == 0 {
			v.Stop.TimeoutSeconds = 5
		}
		if v.Stop.TimeoutSeconds < 1 || v.Stop.TimeoutSeconds > 30 {
			bad(field+".stop.timeout_seconds", "must be 1–30")
		}
		v.Stop.Timeout = time.Duration(v.Stop.TimeoutSeconds) * time.Second
		if v.Open != "" {
			u, e := url.Parse(v.Open)
			if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				bad(field+".open", "must be HTTP/HTTPS URL")
			}
		}
		for _, dep := range v.DockerDependsOn {
			if !identifier.MatchString(dep) {
				bad(field+".docker_depends_on", "invalid Compose service name")
			}
		}
		name := v.Name
		if name == "" {
			name = k
		}
		p.Services = append(p.Services, model.Service{ID: model.ServiceID(p.ID + "/" + k), ProjectID: p.ID, Key: k, Name: name, SourceFile: path, Cwd: cwd, Command: v.Run.Command, Env: v.Env, Ports: v.Ports, Ready: v.Ready, DependsOn: v.DependsOn, DockerDependsOn: v.DockerDependsOn, Stop: v.Stop, Open: v.Open})
	}
	return p, ds
}
