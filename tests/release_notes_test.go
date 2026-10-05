package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseNotesOnlyIncludeSelectedVersion(t *testing.T) {
	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	text := "# Changelog\n\n## [Unreleased]\n\n- FUTURE_FEATURE\n\n## [0.2.0] - 2026-10-05\n\n### Added\n\n- Container metrics.\n\n### Upgrade\n\nKeep your workspace YAML.\n\n## [0.1.0] - 2026-10-04\n\n- OLD_RELEASE\n\n[0.2.0]: https://example.com/compare\n"
	if err := os.WriteFile(changelog, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../scripts/release-notes.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, "0.2.0", changelog)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate version-specific notes: %s %v", out, err)
	}
	for _, want := range []string{"StackHarbor v0.2.0", "Container metrics.", "Keep your workspace YAML.", "v0.2.0"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing release content %q: %s", want, out)
		}
	}
	for _, unwanted := range []string{"FUTURE_FEATURE", "OLD_RELEASE", "[0.2.0]:"} {
		if strings.Contains(string(out), unwanted) {
			t.Fatalf("notes mixed release history: %s", out)
		}
	}
}

func TestReleaseNotesRejectInvalidOrAmbiguousEntries(t *testing.T) {
	for _, tc := range []struct{ name, version, text string }{
		{"missing version", "0.2.0", "## [0.1.0] - 2026-10-05\n- Old release\n"},
		{"duplicate version", "0.2.0", "## [0.2.0] - 2026-10-05\n- First\n## [0.2.0] - 2026-10-05\n- Second\n"},
		{"empty entry", "0.2.0", "## [0.2.0] - 2026-10-05\n\n## [0.1.0] - 2026-10-05\n- Old\n"},
		{"undated entry", "0.2.0", "## [0.2.0] - Unreleased\n- Pending\n"},
		{"invalid version", "../../invalid", "## [0.2.0] - 2026-10-05\n- New\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
			if err := os.WriteFile(changelog, []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "../scripts/release-notes.sh", tc.version, changelog)
			out, err := cmd.Output()
			if err == nil || len(out) != 0 {
				t.Fatalf("invalid release must fail without publishing partial notes: %q %v", out, err)
			}
		})
	}
}
