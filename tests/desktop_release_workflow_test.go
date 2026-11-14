package tests

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDesktopReleaseWorkflowPreservesSigningGatesAndInternalIsolation(t *testing.T) {
	read := func(name string) map[string]any {
		t.Helper()
		data, err := os.ReadFile("../.github/workflows/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	job := func(document map[string]any, id string) map[string]any {
		t.Helper()
		return document["jobs"].(map[string]any)[id].(map[string]any)
	}
	steps := func(value map[string]any) string {
		t.Helper()
		data, err := yaml.Marshal(value["steps"])
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	release := read("release.yml")
	desktop := read("desktop.yml")
	caller := job(release, "macos")
	if caller["needs"] != "check" || caller["uses"] != "./.github/workflows/desktop.yml" || caller["with"].(map[string]any)["public_release"] != true {
		t.Fatal("signed macOS delivery must follow required checks", caller)
	}
	publish := job(release, "publish")
	if dependencies, err := yaml.Marshal(publish["needs"]); err != nil || !strings.Contains(string(dependencies), "check") || !strings.Contains(string(dependencies), "macos") {
		t.Fatal("publication can bypass signed Mac delivery", err)
	}
	publicSteps := steps(publish)
	for _, expected := range []string{"pattern: stackharbor-macos-*-signed", "stage-macos-release.py", "_linux_*.tar.gz", "*.dmg", "desktop-*-build.json"} {
		if !strings.Contains(publicSteps, expected) {
			t.Fatal("release delivery contract missing", expected)
		}
	}
	signed := job(desktop, "signed-macos")
	if signed["environment"] != "macos-release" {
		t.Fatal("release signing must use protected environment")
	}
	condition, _ := signed["if"].(string)
	for _, expected := range []string{"inputs.public_release", "github.event_name == 'push'", "refs/tags/v"} {
		if !strings.Contains(condition, expected) {
			t.Fatal("public release guard missing", expected)
		}
	}
	if signed["env"].(map[string]any)["STACKHARBOR_DESKTOP_DISTRIBUTION"] != "release" {
		t.Fatal("public artifact build can fall back to internal mode")
	}
	signedSteps := steps(signed)
	for _, expected := range []string{"${GITHUB_REF_NAME#v}", "codesign --verify --strict", "xcrun stapler validate", "spctl --assess --type open", "desktop-native-smoke.py", "engine-lifecycle-smoke.py", "--debug --ci --bundles app", "--app desktop/target/debug/bundle/macos/StackHarbor.app", "stackharbor-macos-${{ matrix.os }}-signed", "security delete-keychain"} {
		if !strings.Contains(signedSteps, expected) {
			t.Fatal("signed release verification gate missing", expected)
		}
	}
	for _, name := range []string{"APPLE_CERTIFICATE_P12", "APPLE_CERTIFICATE_PASSWORD", "APPLE_SIGNING_IDENTITY", "APPLE_ID", "APPLE_PASSWORD", "APPLE_TEAM_ID"} {
		if _, exists := caller["secrets"].(map[string]any)[name]; !exists {
			t.Fatal("release secret is not explicitly passed", name)
		}
		if !strings.Contains(signedSteps, name) {
			t.Fatal("missing secret cannot be rejected before signing", name)
		}
	}
	internal := job(desktop, "desktop")
	if internal["env"].(map[string]any)["STACKHARBOR_DESKTOP_DISTRIBUTION"] != "internal" || !strings.Contains(internal["if"].(string), "!inputs.public_release") {
		t.Fatal("internal builds are not isolated")
	}
	if strings.Contains(steps(internal), "secrets.") {
		t.Fatal("internal CI must not receive signing secrets")
	}
	for _, value := range internal["steps"].([]any) {
		step := value.(map[string]any)
		if strings.Contains(steps(map[string]any{"steps": []any{step}}), "python3 scripts/desktop-smoke.py") && step["if"] != "runner.os == 'Linux'" {
			t.Fatal("legacy master smoke must stay Linux-only")
		}
	}
}
