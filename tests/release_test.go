package tests

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"debug/buildinfo"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	versioninfo "github.com/szhjia/stackharbor/internal/buildinfo"
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
	if runtime.GOOS != "linux" {
		t.Fatal("release archive verification requires Linux with qemu-user for the non-native architecture")
	}
	version := os.Getenv("STACKHARBOR_RELEASE_VERSION")
	if version == "" {
		version = versioninfo.Version
	}
	files, e := filepath.Glob("../dist/stackharbor_" + version + "_*.tar.gz")
	if e != nil || len(files) != 2 {
		t.Fatal("missing target archives", files, e)
	}
	for _, target := range []struct{ arch, emulator string }{
		{"amd64", "qemu-x86_64"},
		{"arm64", "qemu-aarch64"},
	} {
		path := "../dist/stackharbor_" + version + "_linux_" + target.arch + ".tar.gz"
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
		seenFiles := map[string]bool{}
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
			seenFiles[name] = true
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
			if name == "stackharbor" {
				data, err := io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
				extracted := filepath.Join(t.TempDir(), "stackharbor")
				if err := os.WriteFile(extracted, data, 0755); err != nil {
					t.Fatal(err)
				}
				metadata, err := buildinfo.ReadFile(extracted)
				if err != nil {
					t.Fatal(path, "read Go build metadata:", err)
				}
				settings := make(map[string]string, len(metadata.Settings))
				for _, setting := range metadata.Settings {
					settings[setting.Key] = setting.Value
				}
				if settings["GOOS"] != "linux" || settings["GOARCH"] != target.arch {
					t.Fatalf("%s: expected linux/%s binary, got %s/%s", path, target.arch, settings["GOOS"], settings["GOARCH"])
				}
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				command := exec.CommandContext(ctx, extracted, "--version")
				if runtime.GOARCH != target.arch {
					command = exec.CommandContext(ctx, target.emulator, extracted, "--version")
				}
				out, err := command.CombinedOutput()
				cancel()
				expected := "StackHarbor " + version
				if err != nil || strings.TrimSpace(string(out)) != expected {
					t.Fatalf("%s: expected runtime version %q, got %q (%v)", path, expected, strings.TrimSpace(string(out)), err)
				}
				for _, asset := range []string{"../internal/web/dist/index.html", "../internal/web/dist/asset-manifest.json"} {
					expected, err := os.ReadFile(asset)
					if err != nil || !bytes.Contains(data, expected) {
						t.Fatal(path, "missing actual embedded frontend", asset, err)
					}
				}
				bundles, err := filepath.Glob("../internal/web/dist/assets/*.js")
				if err != nil || len(bundles) == 0 {
					t.Fatal("missing JavaScript fixture", err)
				}
				for _, asset := range bundles {
					expected, err := os.ReadFile(asset)
					if err != nil || !bytes.Contains(data, expected) {
						t.Fatal(path, "missing embedded JavaScript", asset, err)
					}
				}
			}
			if name == "stackharbor" && h.Mode&0111 == 0 {
				t.Fatal("binary not executable")
			}
			if name == "RELEASE_NOTES.md" {
				packaged, err := io.ReadAll(r)
				source, sourceErr := os.ReadFile("../dist/RELEASE_NOTES.md")
				if err != nil || sourceErr != nil || string(packaged) != string(source) || !strings.Contains(string(packaged), "StackHarbor v"+version) {
					t.Fatal(path, "release notes do not match this version", err, sourceErr)
				}
			}
		}
		gz.Close()
		f.Close()
		for _, required := range []string{"stackharbor", ".stackharbor-tool", "LICENSE", "README.md", "README.zh-CN.md", "CHANGELOG.md", "RELEASE_NOTES.md", "docs", "THIRD_PARTY_NOTICES", "licenses", "skills", "examples", "scripts"} {
			if !seen[required] {
				t.Fatal(path, "missing", required)
			}
		}
		for _, required := range []string{"docs/web-control.md", "licenses/frontend-NOTICES", "skills/stackharbor/references/web-control.md"} {
			if !seenFiles[required] {
				t.Fatal(path, "missing control docs/licenses", required)
			}
		}
		for _, required := range []string{"scripts/install-skill.sh", "skills/stackharbor/SKILL.md", "skills/stackharbor/references/registration.md", "skills/stackharbor/references/v2.md", "skills/stackharbor/references/maintenance.md", "skills/stackharbor/references/installation.md", "skills/stackharbor/references/docker.md", "skills/stackharbor/references/hot-reload.md", "skills/stackharbor/scripts/dev_watch.py"} {
			if !skillFiles[required] {
				t.Fatal(path, "missing skill install or reference file", required)
			}
		}
	}

}
