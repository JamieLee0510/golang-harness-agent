package context

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Skill struct {
	Name        string
	Description string
	Body        string // Markdown body script
	Dir         string // absolute directory holding this skill's SKILL.md + bundled files
}

type SkillLoder struct {
	skillsDir string
}

// NewSkillLoder anchors skill discovery to the CURRENT WORKING DIRECTORY — the
// project you launch the agent from — independent of the sandbox -workdir. This
// mirrors how mcp.json is resolved (config belongs to the project, not the
// binary's install dir). Skills live under <cwd>/.agent/skills/<name>/SKILL.md.
func NewSkillLoder() *SkillLoder {
	var dir string
	if cwd, err := os.Getwd(); err == nil {
		dir = filepath.Join(cwd, ".agent", "skills")
	}
	return &SkillLoder{skillsDir: dir}
}

func (s *SkillLoder) LoadAll() string {
	return loadSkillsFrom(s.skillsDir)
}

// loadSkillsFrom walks a skills base directory, parsing each <name>/SKILL.md
// into the system-prompt section. Returns "" when the directory is absent or
// holds no skills (so no empty header is injected).
func loadSkillsFrom(skillBaseDir string) string {
	if skillBaseDir == "" {
		return ""
	}
	if _, err := os.Stat(skillBaseDir); os.IsNotExist(err) {
		return ""
	}

	var skills []Skill
	_ = filepath.WalkDir(skillBaseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			// ENOENT is a non-event; surface only real access/IO problems so
			// they're diagnosable instead of silently dropping a skill.
			if !os.IsNotExist(err) {
				fmt.Printf("[SkillLoader] ⚠️ failed to read %s: %v\n", path, err)
			}
			return nil
		}
		skills = append(skills, parseSkillMD(string(content), filepath.Dir(path)))
		return nil
	})

	if len(skills) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n### 可用專業技能（Agent Skills）\n")
	b.WriteString("以下是你擁有的標準化外掛技能，請在符合 description 描述的場景下嚴格遵循其正文指令：\n\n")
	for _, skill := range skills {
		fmt.Fprintf(&b, "### 技能名稱： %s\n", skill.Name)
		fmt.Fprintf(&b, "**觸發條件**: %s\n", skill.Description)
		// Tell the model where this skill's bundled files live, so script-style
		// skills can be invoked with an absolute path (also exposed as
		// ${SKILL_DIR} inside the body).
		if skill.Dir != "" {
			fmt.Fprintf(&b, "**技能目錄 (base dir，附帶的腳本/檔案都在這裡)**: %s\n", skill.Dir)
		}
		b.WriteString("\n**執行指南**:\n")
		b.WriteString(skill.Body)
		b.WriteString("\n\n---\n")
	}
	return b.String()
}

func parseSkillMD(content, dir string) Skill {
	skill := Skill{
		Name:        "Unknow SKill",
		Description: "No description provided",
		Body:        content, // default whole content as body
		Dir:         dir,
	}
	// Simple parsing of the YAML Frontmatter (wrapped by ---)
	if strings.HasPrefix(content, "---\n") || strings.HasPrefix(content, "---\r\n") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) == 3 {
			frontmatter := parts[1]
			skill.Body = strings.TrimSpace(parts[2])

			// Extract metadata line by line
			lines := strings.Split(frontmatter, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "name:") {
					skill.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
				} else if strings.HasPrefix(line, "description:") {
					skill.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
				}

			}

		}
	}

	// Expose the skill's own directory to the body so script-style skills can
	// reference bundled files robustly, regardless of the sandbox -workdir.
	skill.Body = strings.ReplaceAll(skill.Body, "${SKILL_DIR}", dir)

	return skill
}
