package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A schema/ID change or unsupported field in the shipped example must fail
// against the real CLI before agents propagate it into other repositories.
func TestSkillExampleValidatesAndPlansWithoutStartingServices(t *testing.T) {
	b, err := os.ReadFile("../skills/stackharbor/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, tail, found := strings.Cut(string(b), "```yaml\n")
	if !found {
		t.Fatal("missing runnable YAML example")
	}
	example, _, found := strings.Cut(tail, "\n```")
	if !found {
		t.Fatal("unterminated YAML example")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stackharbor.yaml"), []byte(example), 0600); err != nil {
		t.Fatal(err)
	}
	// No pnpm or application is available in this fixture. Registration and
	// planning must still succeed without launching the example's command.
	for _, args := range [][]string{
		{"validate", "--root", root, "--json"},
		{"plan", "start", "--root", root, "--target", "web/service/dev", "--json"},
	} {
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), "PATH="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(args, err, string(out))
		}
		var result map[string]any
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatal("expected JSON contract", err, string(out))
		}
		if result["schema_version"] != float64(2) || !strings.Contains(string(out), "web/service/dev") {
			t.Fatal("example must expose the v2 node usable as a plan target", string(out))
		}
	}
	// Existing workspaces with discovery disabled must import the same v2
	// example explicitly, without forcing old nodes or the workspace to v2.
	workspace := "version: 1\nroot: .\ndiscover: false\nregistrations: [stackharbor.yaml]\n"
	if err := os.WriteFile(filepath.Join(root, "stackharbor.workspace.yaml"), []byte(workspace), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "validate", "--root", root, "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("v1 workspace must accept explicit v2 registration", err, string(out))
	}
	var imported struct {
		Schema int `json:"schema_version"`
		Nodes  []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(out, &imported); err != nil || imported.Schema != 2 || len(imported.Nodes) != 1 || imported.Nodes[0].ID != "web/service/dev" {
		t.Fatal("explicit import lost example node or schema", err, string(out))
	}
}
