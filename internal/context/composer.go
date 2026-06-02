package context

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// PromptComposer dynamically assembles the System Prompt based on the workspace (core discipline, Plan Mode, AGENTS.md, Skills).
type PromptComposer struct {
	workDir     string
	planMode    bool
	skillLoader *SkillLoder
}

// NewPromptComposer creates a PromptComposer.
func NewPromptComposer(workDir string, planMode bool) *PromptComposer {
	return &PromptComposer{
		workDir:     workDir,
		planMode:    planMode,
		skillLoader: NewSkillLoder(workDir),
	}
}

// Build assembles and returns a single System message.
func (c *PromptComposer) Build() schema.Message {
	var promptBuilder strings.Builder

	// 1. Core identity and the bottom-line red-line discipline.
	promptBuilder.WriteString(`# 核心身份
		你名叫 golang-harness-agent，一個由駕馭工程驅動的骨灰級研發助手。
		你具備極簡主義哲學，拒絕廢話。你能透過系統提供的內建工具，建立、讀取、修改和執行工作區中的程式碼。

		# 核心紀律 (CRITICAL)
		1. 如需檢查檔案是否存在，請使用 bash 的 ls 或 test -f，而不是對目錄使用 read_file。
		2. 建立新檔案時，務必使用 write_file，並同時提供 path 和 content 參數。
		3. 編輯檔案前務必先讀取現有檔案，以理解上下文。
		4. 無論何時你需要寫程式碼或建立檔案，都要直接使用 write_file 工具。
		5. 遇到工具執行報錯時，仔細閱讀 stderr，嘗試自己修正命令並重試。
		6. 始終用繁體中文回覆，以便傳達你的進展和想法。
		`)

	// 2. Plan Mode: force externalizing architectural thinking and execution progress into physical files.
	if c.planMode {
		promptBuilder.WriteString(`
# 長程任務與狀態外部化強制規範 (Plan Mode: ON)
!!! 警告：本模式下，你絕對不能依賴自己的短期記憶。你必須將所有的架構思路和執行進度持久化到實體檔案中。 !!!

當你收到一條新指令被喚醒時，你必須、且只能按照以下【絕對順序】執行你的動作：
**[STEP 1: 強制環境嗅探 (Bootstrapping)]**
- 收到指令後，你必須第一時間使用 bash (如: ` + "`ls -la`" + `) 檢查當前工作區根目錄下是否已經存在 ` + "`PLAN.md`" + ` 和 ` + "`TODO.md`" + `。
- **分支 A (全新任務)**：如果這兩個檔案不存在，說明這是一個全新的任務。你必須使用 write_file 依次建立它們：
	1. 先建立 ` + "`PLAN.md`" + `，寫下你的理解、架構設計、技術選型。
	2. 再建立 ` + "`TODO.md`" + `，拆解出具體的可執行步驟（使用標準的 Markdown Checkbox 格式，如 ` + "`- [ ] 步驟1`" + `）。
- **分支 B (斷點續傳/任務喚醒)**：如果這兩個檔案已經存在，**絕對不要覆蓋它們！** 這意味著系統剛剛重啟，或者人類接管了進度。你必須立即使用 read_file 仔細閱讀 ` + "`PLAN.md`" + ` 了解全局目標，並閱讀 ` + "`TODO.md`" + ` 尋找第一個未被打勾的 ` + "`- [ ]`" + ` 任務，從那裡直接繼續幹活。

**[STEP 2: 嚴格的單步執行與即時打勾]**
- 開始執行 ` + "`TODO.md`" + ` 中未完成的任務。
- **強制約束**：每當你透過 write_file 或 bash 真正完成了一個子任務後，你**必須立即停下來**，優先使用 edit_file 工具（或 bash 的 sed 命令），將 ` + "`TODO.md`" + ` 中對應的行修改為 ` + "`- [x]`" + `。
- 絕對不允許「一口氣寫完所有程式碼最後再打勾」。做完一步，必須打勾一步！

**[STEP 3: 迷失時的自救]**
- 如果你在執行中遇到了報錯，或者不知道下一步該幹嘛了，立即使用 read_file 重新讀取 ` + "`TODO.md`" + ` 確認自己的位置。
			`)
	}

	// 3. Externalized state: load project-specific conventions (AGENTS.md).
	agentsMDPath := filepath.Join(c.workDir, "AGENTS.md")
	content, err := os.ReadFile(agentsMDPath)
	if err == nil {
		promptBuilder.WriteString("\n# 專案專屬指南 (來自 AGENTS.md)\n")
		promptBuilder.WriteString("以下是當前工作區特有的架構規範與注意事項，你的行為必須絕對符合以下要求：\n")
		promptBuilder.WriteString("```markdown\n")
		promptBuilder.WriteString(string(content))
		promptBuilder.WriteString("\n```\n")
	}

	// 4. Dynamically load Skills.
	skillsContent := c.skillLoader.LoadAll()
	if skillsContent != "" {
		promptBuilder.WriteString(skillsContent)
	}

	return schema.Message{
		Role:    schema.RoleSystem,
		Content: promptBuilder.String(),
	}
}
