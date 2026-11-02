package desktop

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"github.com/szhjia/stackharbor/internal/sessionhost"
	"github.com/szhjia/stackharbor/internal/web"
)

type readyWriter chan []byte

func (w readyWriter) Write(data []byte) (int, error) {
	w <- append([]byte{}, data...)
	return len(data), nil
}

func testNamespace(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sh-desktop-test-")
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func backend(t *testing.T, ns string) (BackendReady, *io.PipeWriter, <-chan error) {
	t.Helper()
	parent, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	out := make(readyWriter, 1)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- RunBackend(ctx, BackendOptions{Namespace: ns, ExpectedVersion: buildinfo.Version, ProtocolVersion: ProtocolVersion}, parent, out)
	}()
	t.Cleanup(func() {
		cancel()
		writer.Close()
		parent.Close()
		select {
		case <-done:
		case <-time.After(7 * time.Second):
			t.Error("sidecar shutdown timed out")
		}
	})
	select {
	case raw := <-out:
		var ready BackendReady
		if err := json.Unmarshal(raw, &ready); err != nil || ready.Error != "" {
			t.Fatalf("readiness %s: %v", raw, err)
		}
		return ready, writer, done
	case <-time.After(5 * time.Second):
		t.Fatal("no readiness")
	}
	return BackendReady{}, nil, nil
}

func assertServing(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("HTTP %d", response.StatusCode)
	}
}

func TestOwnedBackendParentEOFLeavesSlaveAlive(t *testing.T) {
	ns := testNamespace(t)
	t.Setenv("STACKHARBOR_CACHE_DIR", ns)
	host, err := sessionhost.New(context.Background(), model.Workspace{Root: testNamespace(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close(context.Background()) })
	ready, parent, done := backend(t, ns)
	if !ready.Owned || ready.Namespace != ns || ready.BuildVersion != buildinfo.Version || ready.SessionProtocol != sessionapi.ProtocolVersion {
		t.Fatalf("metadata %+v", ready)
	}
	assertServing(t, ready.URL)
	parent.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("owned gateway survived parent EOF")
	}
	client, err := sessionapi.Connect(context.Background(), host.SessionInfo())
	if err != nil {
		t.Fatal("slave was closed", err)
	}
	defer client.Close()
	if _, err := client.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: time.Second}).Get(ready.URL)
	if err == nil {
		response.Body.Close()
		t.Fatal("owned gateway still accepts requests")
	}
}

func TestReusedBackendNeverStopsExternalGateway(t *testing.T) {
	ns := testNamespace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	urls := make(chan string, 1)
	owner := make(chan error, 1)
	go func() {
		owner <- web.Run(ctx, web.Options{Namespace: ns, NoOpen: true, PortExplicit: true, Port: 0, Ready: func(url string) { urls <- url }})
	}()
	var url string
	select {
	case url = <-urls:
	case err := <-owner:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("owner not ready")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-owner:
		case <-time.After(6 * time.Second):
			t.Error("external owner cleanup")
		}
	})
	ready, parent, done := backend(t, ns)
	if ready.Owned || ready.URL != url {
		t.Fatalf("reused metadata %+v", ready)
	}
	parent.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reuse helper did not exit")
	}
	assertServing(t, url)
}

func TestBackendRejectsMismatchedBuildBeforeStarting(t *testing.T) {
	for _, o := range []BackendOptions{{ExpectedVersion: "old", ProtocolVersion: ProtocolVersion}, {ExpectedVersion: buildinfo.Version, ProtocolVersion: 99}} {
		out := make(readyWriter, 1)
		if err := RunBackend(context.Background(), o, &emptyReader{}, out); err == nil {
			t.Fatal("mismatch accepted")
		}
		var ready BackendReady
		if err := json.Unmarshal(<-out, &ready); err != nil || ready.Error == "" || ready.Owned {
			t.Fatalf("failed handshake %+v %v", ready, err)
		}
	}
}

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

func TestNamespaceCanonicalAndPrivate(t *testing.T) {
	dir := testNamespace(t)
	t.Setenv("STACKHARBOR_CACHE_DIR", dir)
	if got, err := namespace(); err != nil || got != dir {
		t.Fatalf("%s %v", got, err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := namespace(); err != nil {
		t.Fatal("existing readable registry rejected", err)
	}
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := namespace(); err == nil {
		t.Fatal("unsafe namespace accepted")
	}
}
