package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestV2TaskAndExplicitDependency(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stackharbor.yaml")
	os.WriteFile(path, []byte(`version: 2
project: {id: app, name: App}
context: {cwd: .}
tasks:
  inspect:
    effect: read-only
    policy: always
    run: {command: [python, check.py]}
services:
  api:
    run: {command: [python, server.py]}
    requires: [{node: app/task/inspect, condition: succeeded}]
`), 0600)
	p, ds := LoadProject(path, root)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if len(p.Services) != 2 {
		t.Fatal("task omitted", p)
	}
	found := false
	for _, n := range p.Services {
		if string(n.ID) == "app/task/inspect" {
			found = true
		}
	}
	if !found {
		t.Fatal("typed task identity absent", p)
	}
}

type countingReader struct {
	io.Reader
	read int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}

func TestEnvDataReadIsBounded(t *testing.T) {
	reader := &countingReader{Reader: bytes.NewReader(bytes.Repeat([]byte{'x'}, 2<<20))}
	data, err := readEnvData(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 1<<20+1 || reader.read != 1<<20+1 {
		t.Fatalf("read %d bytes, returned %d; expected limit plus one", reader.read, len(data))
	}
}
