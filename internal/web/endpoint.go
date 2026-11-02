package web

import (
	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/sessionapi"
)

// Endpoint is emitted only after the current gateway's ownership and transport
// have been established. Owned is relative to the invoking process.
type Endpoint struct {
	ProtocolVersion int    `json:"protocol_version"`
	BuildVersion    string `json:"build_version"`
	SessionProtocol int    `json:"session_protocol"`
	Namespace       string `json:"namespace"`
	URL             string `json:"url"`
	Owned           bool   `json:"owned"`
}

// ResolveNamespace preserves the existing registry policy: it may be readable,
// but must be owned by this user and not writable by other users. Socket runtime
// directories remain subject to their separate, stricter 0700 checks.
func ResolveNamespace(namespace string) (string, error) { return namespacePath(namespace) }

func endpoint(namespace string, port int, owned bool) Endpoint {
	return Endpoint{ProtocolVersion: instanceProtocol, BuildVersion: buildinfo.Version,
		SessionProtocol: sessionapi.ProtocolVersion, Namespace: namespace,
		URL: bootstrapURL(port, ""), Owned: owned}
}
