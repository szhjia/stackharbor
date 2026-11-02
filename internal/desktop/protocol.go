// Package desktop integrates an optional desktop master without transferring
// workspace process ownership out of the foreground terminal.
package desktop

import "github.com/szhjia/stackharbor/internal/web"

const ProtocolVersion = 1

type Request struct {
	ProtocolVersion int    `json:"protocol_version"`
	BuildVersion    string `json:"build_version"`
	Namespace       string `json:"namespace"`
	Background      bool   `json:"background"`
}

type Reply struct {
	ProtocolVersion int    `json:"protocol_version"`
	BuildVersion    string `json:"build_version"`
	Error           string `json:"error,omitempty"`
}

type BackendReady struct {
	web.Endpoint
	Error string `json:"error,omitempty"`
}
