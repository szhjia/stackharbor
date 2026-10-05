package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalledCommandDiscoversCurrentProject(t *testing.T) {
	bundle := t.TempDir()
	installer, err := os.ReadFile("../scripts/install.sh")
	if err != nil {
		t.Fatal("missing one-command installer", err)
	}
	if err = os.Mkdir(filepath.Join(bundle, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bundle, "scripts/install.sh"), installer, 0755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bundle, "stackharbor"), executable, 0755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(t.TempDir(), "local bin")
	install := exec.Command("sh", filepath.Join(bundle, "scripts/install.sh"), binDir)
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatal("install failed", err, string(out))
	}
	root := t.TempDir()
	if err = os.Mkdir(filepath.Join(root, "app"), 0755); err != nil {
		t.Fatal(err)
	}
	project := "version: 1\nproject: {id: current, name: Current project}\nservices:\n  dev:\n    run: {command: [echo, ready]}\n"
	if err = os.WriteFile(filepath.Join(root, "app/stackharbor.yaml"), []byte(project), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := "version: 1\nroot: .\ndiscover: false\nregistrations: [app/stackharbor.yaml]\n"
	if err = os.WriteFile(filepath.Join(root, "stackharbor.workspace.yaml"), []byte(workspace), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "stackharbor discover --json")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("installed command failed", err, string(out))
	}
	var result struct {
		Root     string `json:"root"`
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatal(err, string(out))
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	if result.Root != realRoot || len(result.Projects) != 1 || result.Projects[0].ID != "current" {
		t.Fatal("command must use current project and its default workspace", string(out))
	}
	// A second installation must preserve a pre-existing command, even a broken symlink.
	installed := filepath.Join(binDir, "stackharbor")
	if err = os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("missing-command", installed); err != nil {
		t.Fatal(err)
	}
	install = exec.Command("sh", filepath.Join(bundle, "scripts/install.sh"), binDir)
	if out, err = install.CombinedOutput(); err == nil || !strings.Contains(string(out), "already exists") {
		t.Fatal("installer must refuse existing commands", err, string(out))
	}
	if target, err := os.Readlink(installed); err != nil || target != "missing-command" {
		t.Fatal("installer replaced existing command", target, err)
	}
}
