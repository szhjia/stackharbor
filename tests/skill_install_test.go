package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func skillBundle(t *testing.T) string {
	t.Helper()
	bundle := filepath.Join(t.TempDir(), "tool checkout")
	if err := os.MkdirAll(filepath.Join(bundle, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	installer, err := os.ReadFile("../scripts/install-skill.sh")
	if err != nil {
		t.Fatal("missing user skill installer", err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "scripts/install-skill.sh"), installer, 0644); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(bundle, "skills/stackharbor")
	if err := os.MkdirAll(filepath.Join(skill, "references"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SKILL.md", "references/registration.md", "references/v2.md", "references/maintenance.md", "references/installation.md", "references/docker.md"} {
		if err := os.WriteFile(filepath.Join(skill, name), []byte("original "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return bundle
}

func installSkill(bundle, skillsDir string) ([]byte, error) {
	cmd := exec.Command("sh", filepath.Join(bundle, "scripts/install-skill.sh"), skillsDir)
	return cmd.CombinedOutput()
}

func TestSkillInstallLinksWholeDirectoryAndTracksUpgrade(t *testing.T) {
	bundle := skillBundle(t)
	skillsDir := filepath.Join(t.TempDir(), "user skills")
	for i := 0; i < 2; i++ {
		if out, err := installSkill(bundle, skillsDir); err != nil {
			t.Fatal("installation must be idempotent", err, string(out))
		}
	}
	target := filepath.Join(skillsDir, "stackharbor")
	source := filepath.Join(bundle, "skills/stackharbor")
	realSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(target); err != nil || got != realSource {
		t.Fatal("skill must link to versioned source", got, err)
	}
	for _, name := range []string{"SKILL.md", "references/v2.md"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte("upgraded"), 0644); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(filepath.Join(target, name)); err != nil || string(b) != "upgraded" {
			t.Fatal("installed skill did not follow tool upgrade", name, string(b), err)
		}
	}
}

func TestSkillInstallPreservesExistingEntries(t *testing.T) {
	for _, kind := range []string{"directory", "file", "broken-link", "other-link"} {
		t.Run(kind, func(t *testing.T) {
			bundle := skillBundle(t)
			skillsDir := t.TempDir()
			target := filepath.Join(skillsDir, "stackharbor")
			switch kind {
			case "directory":
				if err := os.Mkdir(target, 0755); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(target, []byte("user owned"), 0644); err != nil {
					t.Fatal(err)
				}
			default:
				link := "missing"
				if kind == "other-link" {
					link = bundle
				}
				if err := os.Symlink(link, target); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			if out, err := installSkill(bundle, skillsDir); err == nil || !strings.Contains(string(out), "already exists") {
				t.Fatal("installer must refuse unrelated entries", err, string(out))
			}
			after, err := os.Lstat(target)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("installer changed existing entry", err)
			}
		})
	}
}

func TestSkillInstallExplicitDirectoryAndMissingSource(t *testing.T) {
	bundle := skillBundle(t)
	dir := filepath.Join(t.TempDir(), "codex skills")
	if out, err := installSkill(bundle, dir); err != nil {
		t.Fatal(err, string(out))
	}
	if err := os.Remove(filepath.Join(bundle, "skills/stackharbor/references/v2.md")); err != nil {
		t.Fatal(err)
	}
	missingTarget := filepath.Join(t.TempDir(), "skills")
	if out, err := installSkill(bundle, missingTarget); err == nil {
		t.Fatal("incomplete distribution must fail", string(out))
	}
	if _, err := os.Lstat(missingTarget); !os.IsNotExist(err) {
		t.Fatal("incomplete source must not create installation", err)
	}
}
