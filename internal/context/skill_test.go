package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill creates <base>/<name>/SKILL.md with the given content.
func writeSkill(t *testing.T, base, name, content string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func TestLoadSkillsFrom_ParsesAndInjects(t *testing.T) {
	base := t.TempDir()
	writeSkill(t, base, "code-review", "---\nname: code-review\ndescription: review a diff\n---\nDo the review steps.")
	writeSkill(t, base, "translate", "---\nname: translate\ndescription: translate text\n---\nTranslate carefully.")

	got := loadSkillsFrom(base)

	// The bug being fixed: skills used to never appear because injection ran
	// only on read FAILURE. Assert the bodies and metadata are now present.
	for _, want := range []string{
		"code-review", "review a diff", "Do the review steps.",
		"translate", "translate text", "Translate carefully.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q\n---\n%s", want, got)
		}
	}
}

// TestNewSkillLoder_ResolvesAgentSkillsInCwd verifies the cwd-anchored path:
// NewSkillLoder must discover <cwd>/.agent/skills/<name>/SKILL.md.
func TestNewSkillLoder_ResolvesAgentSkillsInCwd(t *testing.T) {
	project := t.TempDir()
	writeSkill(t, filepath.Join(project, ".agent", "skills"), "demo",
		"---\nname: demo\ndescription: a demo skill\n---\nFollow these steps.")

	t.Chdir(project) // launch "from the project dir"

	got := NewSkillLoder().LoadAll()
	for _, want := range []string{"demo", "a demo skill", "Follow these steps."} {
		if !strings.Contains(got, want) {
			t.Fatalf("cwd-based .agent/skills not loaded, missing %q\n---\n%s", want, got)
		}
	}
}

func TestLoadSkillsFrom_EmptyOrMissing(t *testing.T) {
	t.Run("missing directory yields empty (no header)", func(t *testing.T) {
		if got := loadSkillsFrom(filepath.Join(t.TempDir(), "nope")); got != "" {
			t.Fatalf("expected empty, got: %q", got)
		}
	})

	t.Run("present but no skills yields empty (no orphan header)", func(t *testing.T) {
		base := t.TempDir() // exists, but contains no SKILL.md
		if got := loadSkillsFrom(base); got != "" {
			t.Fatalf("expected empty for skill-less dir, got: %q", got)
		}
	})

	t.Run("empty base path", func(t *testing.T) {
		if got := loadSkillsFrom(""); got != "" {
			t.Fatalf("expected empty, got: %q", got)
		}
	})
}

func TestParseSkillMD(t *testing.T) {
	t.Run("with frontmatter", func(t *testing.T) {
		s := parseSkillMD("---\nname: foo\ndescription: bar\n---\nbody here", "/skills/foo")
		if s.Name != "foo" || s.Description != "bar" || s.Body != "body here" {
			t.Fatalf("got %+v", s)
		}
		if s.Dir != "/skills/foo" {
			t.Fatalf("Dir = %q, want /skills/foo", s.Dir)
		}
	})
	t.Run("without frontmatter falls back to defaults", func(t *testing.T) {
		s := parseSkillMD("just a plain body", "/skills/x")
		if s.Name != "Unknow SKill" || s.Body != "just a plain body" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("${SKILL_DIR} is substituted with the skill dir", func(t *testing.T) {
		s := parseSkillMD("---\nname: s\n---\nrun ${SKILL_DIR}/go.py", "/abs/skills/s")
		if !strings.Contains(s.Body, "/abs/skills/s/go.py") {
			t.Fatalf("SKILL_DIR not substituted: %q", s.Body)
		}
		if strings.Contains(s.Body, "${SKILL_DIR}") {
			t.Fatalf("placeholder left unreplaced: %q", s.Body)
		}
	})
}
