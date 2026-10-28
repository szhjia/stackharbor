package runner

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/szhjia/stackharbor/internal/model"
	"github.com/szhjia/stackharbor/internal/process"
)

// Opt in with an installed Vite CLI; normal Go tests need no Node dependencies.
func TestLocalViteHMR(t *testing.T) {
	vite := os.Getenv("STACKHARBOR_TEST_VITE")
	if vite == "" {
		t.Skip("set STACKHARBOR_TEST_VITE to an installed vite/bin/vite.js")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	root := t.TempDir()
	for name, content := range map[string]string{
		"index.html": `<script type="module" src="/source.js"></script>`,
		"source.js":  `export const value = 'before'; if (import.meta.hot) import.meta.hot.accept();`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h, err := NewLocal(process.NewReader()).Start(ctx, model.Service{
		Command: []string{node, vite, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--strictPort"},
		Cwd:     root, Stop: model.StopPolicy{Signal: "TERM", Timeout: 3 * time.Second},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if result := h.Stop(stopCtx, model.StopPolicy{Signal: "TERM", Timeout: 3 * time.Second}); !result.Complete {
			t.Errorf("Vite cleanup incomplete: %+v", result)
		}
	})
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: time.Second}
	for {
		response, err := client.Get(origin)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case result := <-h.Done():
			t.Fatalf("Vite exited before readiness: %+v", result)
		case <-ctx.Done():
			t.Fatal("Vite readiness timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}
	script := `
import {writeFile} from 'node:fs/promises';
const origin = process.argv[1];
const client = await (await fetch(origin + '/@vite/client')).text();
const token = client.match(/const wsToken = "([^"]+)"/)[1];
await fetch(origin + '/source.js');
await new Promise((resolve, reject) => {
  const socket = new WebSocket(origin.replace('http:', 'ws:') + '/?token=' + token, 'vite-hmr');
  const timer = setTimeout(() => { socket.close(); reject(Error('HMR update timeout')); }, 10000);
  socket.onerror = () => { clearTimeout(timer); reject(Error('HMR WebSocket failed')); };
  socket.onmessage = async ({data}) => {
    const message = JSON.parse(data);
    if (message.type === 'connected') {
      await writeFile('source.js', "export const value = 'after'; if (import.meta.hot) import.meta.hot.accept();");
    }
    if (message.type === 'update' && message.updates.some(update => update.path === '/source.js')) {
      clearTimeout(timer); socket.close(); resolve();
    }
  };
});
if (!(await (await fetch(origin + '/source.js')).text()).includes("'after'")) throw Error('stale source');
console.log('Vite source edit delivered through HMR WebSocket under StackHarbor runner');
`
	check := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, origin)
	check.Dir = root
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("HMR check: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
