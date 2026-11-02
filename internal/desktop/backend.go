package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/web"
)

type BackendOptions struct {
	Namespace       string
	ExpectedVersion string
	ProtocolVersion int
}

// RunBackend binds only this invocation's gateway to the parent's pipe. EOF
// never closes workspace sessions or a reused gateway owned by another process.
func RunBackend(ctx context.Context, o BackendOptions, parent io.Reader, out io.Writer) error {
	emitted := false
	run := func() error {
		if o.ProtocolVersion != ProtocolVersion || o.ExpectedVersion != buildinfo.Version {
			return errors.New("desktop/CLI version or protocol mismatch; install matching builds")
		}
		childCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			_, _ = io.Copy(io.Discard, parent)
			cancel()
		}()
		return web.Run(childCtx, web.Options{Namespace: o.Namespace, PortExplicit: true, Port: 0, NoOpen: true,
			EndpointReady: func(v web.Endpoint) error {
				if v.BuildVersion != buildinfo.Version || v.ProtocolVersion != ProtocolVersion || v.SessionProtocol != sessionapi.ProtocolVersion {
					return errors.New("existing Web gateway is incompatible; it was left running")
				}
				if err := json.NewEncoder(out).Encode(BackendReady{Endpoint: v}); err != nil {
					return fmt.Errorf("desktop parent handshake: %w", err)
				}
				emitted = true
				return nil
			}})
	}
	err := run()
	if err != nil && !emitted {
		_ = json.NewEncoder(out).Encode(BackendReady{Error: err.Error()})
	}
	return err
}
