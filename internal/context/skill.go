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
}

type SkillLoder struct {
	workDir string
}

func NewSkillLoder(workDir string) *SkillLoder {
	return &SkillLoder{
		workDir: workDir,
	}
}

func (s *SkillLoder) LoadAll() string {
	skillBaseDir := filepath.Join(s.workDir, ".claw", "skills")

	// if no skills folder, means there are no skill in workspace, return empty string
	if _, err := os.Stat(skillBaseDir); os.IsNotExist(err) {
		return ""
	}

	var skillsBuilder strings.Builder
	skillsBuilder.WriteString("\n### 可用專業技能（Agent Skills）\n")
	skillsBuilder.WriteString("以下是你擁有的標準化外掛技能，請在符合 description 描述的場景下嚴格遵循其正文指令：\n\n")

	err := filepath.WalkDir(skillBaseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && d.Name() == "SKILL.md" {
			content, err := os.ReadFile(path)
			if err != nil {
				skill := parseSkillMD(string(content))

				// Inject the parsed skill in structured form
				skillsBuilder.WriteString(fmt.Sprintf("### 技能名稱： %s\n", skill.Name))
				skillsBuilder.WriteString(fmt.Sprintf("**觸發條件**: %s\n\n", skill.Description))
				skillsBuilder.WriteString("**執行指南**:\n")
				skillsBuilder.WriteString(skill.Body)
				skillsBuilder.WriteString("\n\n---\n")
			}
		}
		return nil
	})

	if err != nil || skillsBuilder.Len() < 100 {
		return ""
	}
	return skillsBuilder.String()
}

func parseSkillMD(content string) Skill {
	skill := Skill{
		Name:        "Unknow SKill",
		Description: "No description provided",
		Body:        content, // default whole content as body
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

	return skill
}
