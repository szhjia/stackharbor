package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebCLIRejectsInvalidFlagsAndShowsHelp(t *testing.T) {
	for _, args := range [][]string{{"web", "--port", "-1"}, {"web", "--port", "65536"}, {"web", "--root", "/missing"}, {"web", "extra"}} {
		var out, errs bytes.Buffer
		if code := Run(context.Background(), args, strings.NewReader(""), &out, &errs); code != 2 {
			t.Fatalf("%v code=%d", args, code)
		}
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"web", "--help"}, strings.NewReader(""), &out, &errs); code != 0 || !strings.Contains(out.String(), "--no-open") {
		t.Fatalf("help code=%d out=%s", code, out.String())
	}
}
func TestWebCLIStartsWithoutWorkspaceDiscovery(t *testing.T) {
	cache, err := os.MkdirTemp("/tmp", "sh-web-cli-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cache)
	cache, _ = filepath.EvalSymlinks(cache)
	t.Setenv("STACKHARBOR_CACHE_DIR", cache)
	root := t.TempDir()
	t.Chdir(root)
	os.WriteFile(filepath.Join(root, "stackharbor.workspace.yaml"), []byte("invalid: ["), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan int, 1)
	var errs bytes.Buffer
	go func() {
		defer writer.Close()
		done <- Run(ctx, []string{"web", "--port", "0", "--no-open"}, strings.NewReader(""), writer, &errs)
	}()
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "http://127.0.0.1:") || !strings.Contains(line, "/#token=") {
		cancel()
		t.Fatalf("startup %q %v", line, err)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit%d err=%s", code, errs.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("web did not close")
	}
}
