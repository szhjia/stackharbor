package web

import (
	"context"
	"errors"
	"testing"

	"github.com/szhjia/stackharbor/internal/buildinfo"
)

func TestDesktopReadinessOwnershipAndRejectedReuse(t *testing.T) {
	ns := namespace(t)
	var owned Endpoint
	url, _, _ := launch(t, Options{Namespace: ns, NoOpen: true, PortExplicit: true, Port: 0, EndpointReady: func(e Endpoint) error { owned = e; return nil }})
	if !owned.Owned || owned.URL != url || owned.Namespace != ns || owned.BuildVersion != buildinfo.Version {
		t.Fatalf("owned %+v", owned)
	}
	want := errors.New("desktop refuses this endpoint")
	err := Run(context.Background(), Options{Namespace: ns, NoOpen: true, EndpointReady: func(e Endpoint) error {
		if e.Owned || e.URL != url || e.Namespace != ns {
			t.Fatalf("reused %+v", e)
		}
		return want
	}})
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
	if err := Run(context.Background(), Options{Namespace: ns, NoOpen: true}); err != nil {
		t.Fatal("reuse rejection stopped owner", err)
	}
}
