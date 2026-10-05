package tests

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseDownloaderVerifiesBeforeInstalling(t *testing.T) {
	for _, scenario := range []string{"valid", "corrupt", "missing-checksum", "existing", "invalid-version", "network-failure"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			fake := filepath.Join(root, "tools")
			if err := os.MkdirAll(fake, 0755); err != nil {
				t.Fatal(err)
			}
			archive := fmt.Sprintf("stackharbor_0.1.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
			f, err := os.Create(filepath.Join(root, archive))
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			payload := []byte("#!/bin/sh\nprintf 'StackHarbor fixture\\n'\n")
			if err := tw.WriteHeader(&tar.Header{Name: "stackharbor", Mode: 0755, Size: int64(len(payload))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(payload); err != nil {
				t.Fatal(err)
			}
			tw.Close()
			gz.Close()
			f.Close()
			data, err := os.ReadFile(filepath.Join(root, archive))
			if err != nil {
				t.Fatal(err)
			}
			manifest := fmt.Sprintf("%x  %s\n", sha256.Sum256(data), archive)
			if scenario == "corrupt" {
				manifest = strings.Repeat("0", 64) + "  " + archive + "\n"
			}
			if scenario == "missing-checksum" {
				manifest = strings.Repeat("0", 64) + "  other.tar.gz\n"
			}
			if err := os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			curl := `#!/bin/sh
set -eu
if [ "$SH_DOWNLOAD_CASE" = network-failure ]; then exit 22; fi
url=; output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output=$2; shift 2;;
    --proto|--proto-redir) shift 2;;
    https://*) url=$1; shift;;
    *) shift;;
  esac
done
case "$url" in
 https://api.github.com/repos/szhjia/stackharbor/releases/latest) printf '{"tag_name":"v0.1.0"}\n';;
 https://github.com/szhjia/stackharbor/releases/download/v0.1.0/*) cp "$SH_DOWNLOAD_FIXTURE/${url##*/}" "$output";;
 *) exit 42;;
esac
`
			if err := os.WriteFile(filepath.Join(fake, "curl"), []byte(curl), 0755); err != nil {
				t.Fatal(err)
			}
			if scenario == "existing" {
				os.MkdirAll(bin, 0755)
				os.WriteFile(filepath.Join(bin, "stackharbor"), []byte("keep me"), 0755)
			}
			version := "latest"
			if scenario == "invalid-version" {
				version = "../../invalid"
			}
			cmd := exec.Command("sh", "../scripts/install-release.sh", version, bin)
			cmd.Env = append(os.Environ(), "PATH="+fake+":"+os.Getenv("PATH"), "SH_DOWNLOAD_FIXTURE="+root, "SH_DOWNLOAD_CASE="+scenario)
			out, err := cmd.CombinedOutput()
			installed, readErr := os.ReadFile(filepath.Join(bin, "stackharbor"))
			if scenario == "valid" {
				if err != nil || readErr != nil || string(installed) != string(payload) {
					t.Fatalf("%v %v %s", err, readErr, out)
				}
				info, _ := os.Stat(filepath.Join(bin, "stackharbor"))
				if info.Mode()&0111 == 0 {
					t.Fatal("not executable")
				}
			} else {
				if err == nil {
					t.Fatalf("expected failure: %s", out)
				}
				if scenario == "existing" {
					if string(installed) != "keep me" {
						t.Fatal("existing command changed")
					}
				} else if !os.IsNotExist(readErr) {
					t.Fatal("failed download published a binary", readErr)
				}
			}
		})
	}
}
