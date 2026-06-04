package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/JamieLee0510/go-agent-harness/internal/utils"
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

// fuzzyReplace implements a four-level fault-tolerant fallback string replacement algorithm.
func fuzzyReplace(originalContent, oldText, newText string) (string, error) {
	// L1: exact match
	count := strings.Count(originalContent, oldText)
	if count == 1 {
		return strings.Replace(originalContent, oldText, newText, 1), nil
	}
	if count > 1 {
		return "", fmt.Errorf("old_text 匹配到了 %d 處，請提供更多的上下文程式碼以確保唯一性", count)
	}

	// L2: newline normalization (uniformly convert \r\n to \n)
	normalizedContent := strings.ReplaceAll(originalContent, "\r\n", "\n")
	normalizedOld := strings.ReplaceAll(oldText, "\r\n", "\n")

	count = strings.Count(normalizedContent, normalizedOld)
	if count == 1 {
		return strings.Replace(normalizedContent, normalizedOld, newText, 1), nil
	}

	// L3: Trim Space match (ignore leading/trailing blank lines and spaces)
	trimmedOld := strings.TrimSpace(normalizedOld)
	if trimmedOld != "" {
		count = strings.Count(normalizedContent, trimmedOld)
		if count == 1 {
			// Note: when replacing here, we can only replace the trimmed part; we cannot directly use newText and destroy the original indentation.
			// To keep this column's code from being overly verbose and complex, when L3/L4 is triggered, if newText does not carry the correct indentation,
			// the replaced code may look poorly formatted. But this is still better than throwing an error and letting the Agent loop forever.
			return strings.Replace(normalizedContent, trimmedOld, newText, 1), nil
		}
	}

	// L4: line-by-line de-indented match (strongest fault tolerance: eliminates the LLM's hallucination of omitting indentation)
	return lineByLineReplace(normalizedContent, normalizedOld, newText)
}

// lineByLineReplace splits the text by line, trims leading/trailing whitespace, then performs sliding-window matching
func lineByLineReplace(content, oldText, newText string) (string, error) {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(strings.TrimSpace(oldText), "\n")

	if len(oldLines) == 0 || len(contentLines) < len(oldLines) {
		return "", fmt.Errorf("找不到該程式碼片段")
	}

	// Clean up the leading/trailing whitespace of each line in oldLines
	for i := range oldLines {
		oldLines[i] = strings.TrimSpace(oldLines[i])
	}

	matchCount := 0
	matchStartIndex := -1
	matchEndIndex := -1

	// Sliding window to find matching blocks in the original file
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

	// Perform the replacement: replace the matched original line range with the split lines of newText
	// (handled simply here, inserting newText as a whole)
	var newContentLines []string
	newContentLines = append(newContentLines, contentLines[:matchStartIndex]...)
	newContentLines = append(newContentLines, newText) // insert new content
	newContentLines = append(newContentLines, contentLines[matchEndIndex:]...)

	return strings.Join(newContentLines, "\n"), nil
}

func (t *EditFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input editFileArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("參數解析失敗: %w", err)
	}

	fullPath, err := utils.ResolvePath(t.workDir, input.Path)
	if err != nil {
		return "", err
	}

	// 1. Read the original file content
	contentBytes, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("讀取檔案失敗，請確認路徑是否正確: %w", err)
	}
	originalContent := string(contentBytes)

	// 2. Call the multi-level fuzzy replacement algorithm
	newContent, err := fuzzyReplace(originalContent, input.OldText, input.NewText)
	if err != nil {
		// Return the specific error reason (e.g. multiple matches) as-is, so the LLM can correct itself.
		return "", err
	}

	// 3. Safely write the new content back to disk
	if err := os.WriteFile(fullPath, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("寫回檔案失敗: %w", err)
	}

	return fmt.Sprintf("✅ 成功修改檔案: %s", input.Path), nil
}
