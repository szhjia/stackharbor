package tests

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptsUseIndependentRoot(t *testing.T) {
	root, _ := filepath.Abs("..")
	cmd := exec.Command(filepath.Join(root, "scripts", "check.sh"), "--print-root")
	cmd.Dir = t.TempDir()
	out, e := cmd.CombinedOutput()
	if e != nil || strings.TrimSpace(string(out)) != root {
		t.Fatal("script does not resolve independent root", e, string(out))
	}
}
func TestReleaseArchiveContentsAndVersion(t *testing.T) {
	if os.Getenv("STACKHARBOR_RELEASE_TEST") != "1" {
		t.Skip("release artifacts checked after packaging")
	}
	version := os.Getenv("STACKHARBOR_RELEASE_VERSION")
	if version == "" {
		version = "0.1.0"
	}
	files, e := filepath.Glob("../dist/stackharbor_" + version + "_*.tar.gz")
	if e != nil || len(files) != 4 {
		t.Fatal("missing target archives", files, e)
	}
	for _, path := range files {
		f, e := os.Open(path)
		if e != nil {
			t.Fatal(e)
		}
		gz, e := gzip.NewReader(f)
		if e != nil {
			t.Fatal(e)
		}
		r := tar.NewReader(gz)
		seen := map[string]bool{}
		skillFiles := map[string]bool{}
		for {
			h, e := r.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			name := strings.TrimPrefix(h.Name, "./")
			seen[strings.Split(name, "/")[0]] = true
			if h.Typeflag == tar.TypeReg && (strings.HasPrefix(name, "skills/stackharbor/") || name == "scripts/install-skill.sh") {
				packaged, err := io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
				source, err := os.ReadFile(filepath.Join("..", name))
				if err != nil || string(packaged) != string(source) {
					t.Fatal(path, "skill distribution differs from repository", name, err)
				}
				skillFiles[name] = true
			}
			if name == "stackharbor" && h.Mode&0111 == 0 {
				t.Fatal("binary not executable")
			}
		}
		gz.Close()
		f.Close()
		for _, required := range []string{"stackharbor", ".stackharbor-tool", "LICENSE", "README.md", "README.zh-CN.md", "docs", "THIRD_PARTY_NOTICES", "licenses", "skills", "examples", "scripts"} {
			if !seen[required] {
				t.Fatal(path, "missing", required)
			}
		}
		for _, required := range []string{"scripts/install-skill.sh", "skills/stackharbor/SKILL.md", "skills/stackharbor/references/registration.md", "skills/stackharbor/references/v2.md", "skills/stackharbor/references/maintenance.md", "skills/stackharbor/references/installation.md", "skills/stackharbor/references/docker.md"} {
			if !skillFiles[required] {
				t.Fatal(path, "missing skill install or reference file", required)
			}
		}
	}
	out, e := exec.Command("../dist/stackharbor", "--version").CombinedOutput()
	if e != nil || !strings.Contains(string(out), version) {
		t.Fatal("archive version mismatch", e, string(out))
	}
}
