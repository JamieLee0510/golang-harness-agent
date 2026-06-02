package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

type EditFileTool struct {
	workDir string
}

func NewEditFileTool(workDir string) *EditFileTool {
	return &EditFileTool{
		workDir: workDir,
	}
}

func (t *EditFileTool) Name() string {
	return "edit_file"
}

func (t *EditFileTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Perform partial string replacements on the existing file. This is safer and faster than rewriting the entire file. Please provide sufficient old_text context to ensure uniqueness of the matches.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The file path to be modified",
				},
				"old_text": map[string]any{
					"type":        "string",
					"description": "The original text in the file. It must contain sufficient context (it is recommended to include several lines above and below) to ensure its uniqueness within the file.",
				},
				"new_text": map[string]any{
					"type":        "string",
					"description": "The new text to be replaced.",
				},
			},
			"required": []string{"path", "old_text", "new_text"},
		},
	}
}

type editFileArgs struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// fuzzyReplace 實作四級容錯降級的字串替換演算法。
func fuzzyReplace(originalContent, oldText, newText string) (string, error) {
	// L1: 精確匹配
	count := strings.Count(originalContent, oldText)
	if count == 1 {
		return strings.Replace(originalContent, oldText, newText, 1), nil
	}
	if count > 1 {
		return "", fmt.Errorf("old_text 匹配到了 %d 處，請提供更多的上下文程式碼以確保唯一性", count)
	}

	// L2: 換行符歸一化 (統一將 \r\n 轉換為 \n)
	normalizedContent := strings.ReplaceAll(originalContent, "\r\n", "\n")
	normalizedOld := strings.ReplaceAll(oldText, "\r\n", "\n")

	count = strings.Count(normalizedContent, normalizedOld)
	if count == 1 {
		return strings.Replace(normalizedContent, normalizedOld, newText, 1), nil
	}

	// L3: Trim Space 匹配 (忽略首尾的空行和空格)
	trimmedOld := strings.TrimSpace(normalizedOld)
	if trimmedOld != "" {
		count = strings.Count(normalizedContent, trimmedOld)
		if count == 1 {
			// 注意：這裡替換時，我們只能替換被 Trim 後的部分，不能直接用 newText 破壞原本的縮排
			// 為了保持本專欄程式碼不過於冗長複雜，當觸發 L3/L4 時，如果 newText 沒有帶有正確的縮排，
			// 可能會導致替換後程式碼格式不美觀。但這總比直接報錯讓 Agent 死循環要好。
			return strings.Replace(normalizedContent, trimmedOld, newText, 1), nil
		}
	}

	// L4: 逐行去縮排匹配 (最強力的容錯：消除大模型遺漏縮排的幻覺)
	return lineByLineReplace(normalizedContent, normalizedOld, newText)
}

// lineByLineReplace 將文字按行切割，去除首尾空白後進行滑動視窗匹配
func lineByLineReplace(content, oldText, newText string) (string, error) {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(strings.TrimSpace(oldText), "\n")

	if len(oldLines) == 0 || len(contentLines) < len(oldLines) {
		return "", fmt.Errorf("找不到該程式碼片段")
	}

	// 清理 oldLines 的每行首尾空白
	for i := range oldLines {
		oldLines[i] = strings.TrimSpace(oldLines[i])
	}

	matchCount := 0
	matchStartIndex := -1
	matchEndIndex := -1

	// 滑動視窗在原始檔案中尋找匹配塊
	for i := 0; i <= len(contentLines)-len(oldLines); i++ {
		isMatch := true
		for j := range len(oldLines) {
			if strings.TrimSpace(contentLines[i+j]) != oldLines[j] {
				isMatch = false
				break
			}
		}

		if isMatch {
			matchCount++
			matchStartIndex = i
			matchEndIndex = i + len(oldLines)
		}
	}

	if matchCount == 0 {
		return "", fmt.Errorf("在檔案中未找到 old_text，請大模型先呼叫 read_file 仔細確認檔案內容和縮排")
	}
	if matchCount > 1 {
		return "", fmt.Errorf("模糊匹配到了 %d 處相似程式碼，請提供更多上下行程式碼以精確定位", matchCount)
	}

	// 執行替換：將匹配到的原始行範圍替換為 newText 拆分後的行
	// (這裡簡單處理，將 newText 直接作為整體替換進去)
	var newContentLines []string
	newContentLines = append(newContentLines, contentLines[:matchStartIndex]...)
	newContentLines = append(newContentLines, newText) // 插入新內容
	newContentLines = append(newContentLines, contentLines[matchEndIndex:]...)

	return strings.Join(newContentLines, "\n"), nil
}

func (t *EditFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input editFileArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("參數解析失敗: %w", err)
	}

	fullPath := filepath.Join(t.workDir, input.Path)

	// 1. 讀取原檔案內容
	contentBytes, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("讀取檔案失敗，請確認路徑是否正確: %w", err)
	}
	originalContent := string(contentBytes)

	// 2. 呼叫多級模糊替換演算法
	newContent, err := fuzzyReplace(originalContent, input.OldText, input.NewText)
	if err != nil {
		// 將具體的報錯原因（如匹配到多處）原樣返回，讓大模型自行糾正。
		return "", err
	}

	// 3. 將新內容安全地寫回磁碟
	if err := os.WriteFile(fullPath, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("寫回檔案失敗: %w", err)
	}

	return fmt.Sprintf("✅ 成功修改檔案: %s", input.Path), nil
}
