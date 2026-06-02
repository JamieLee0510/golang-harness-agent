package context

import (
	"fmt"
	"strings"
)

// For maximum simplicity and clarity, this uses keyword matching based on strings.Contains.
//
// Note: in production, branching logic on fuzzy Chinese error strings is an extremely fragile (flaky)
// anti-pattern -- changing a single character in an underlying tool's error message breaks the entire
// self-healing mechanism. The industrial-grade approach is to have underlying tools raise stable
// POSIX standard errors (such as no such file or directory, permission denied), or define a set of
// domain error codes at the Tool Registry layer (such as ERR_FILE_NOT_FOUND, ERR_EDIT_FUZZY_MATCH_FAILED),
// so the model receives both the error text and the error code. This only demonstrates how the Harness
// intercepts and injects at the architectural layer; for enterprise deployment, refactor this into a
// switch-case based on standardized Error Codes.

// RecoveryManager injects corrective suggestions for the model based on error signatures when a tool execution fails.
type RecoveryManager struct{}

// NewRevocerManager creates a RecoveryManager.
func NewRevocerManager() *RecoveryManager {
	return &RecoveryManager{}
}

// AnalyzeAndInject matches the error signature of toolName and rawError; on a hit it appends a corrective
// suggestion after the original error; otherwise it returns rawError unchanged.
func (rm *RecoveryManager) AnalyzeAndInject(toolName string, rawError string) string {
	var hint string

	lowerError := strings.ToLower(rawError)

	switch toolName {
	case "edit_file":
		// Match the fixed error message thrown by fuzzyReplace.
		if strings.Contains(rawError, "在檔案中未找到 old_text") || strings.Contains(rawError, "找不到該程式碼片段") {
			hint = "你提供的 old_text 與檔案當前內容不一致，或者缺少必要的縮排。請先使用 `read_file` 工具重新讀取該檔案，取得最新、準確的內容後，再重新發起編輯。"
		} else if strings.Contains(rawError, "匹配到了多處") || strings.Contains(rawError, "提供更多上下文") {
			hint = "你的 old_text 不夠具體，命中了多個相同程式碼塊。請在 old_text 中增加上下相鄰的幾行程式碼，以確保替換的唯一性。"
		}
	case "read_file", "write_file":
		// Match POSIX standard errors thrown by the Go os package.
		if strings.Contains(lowerError, "no such file or directory") {
			hint = "路徑似乎不正確。請不要憑空猜測，先使用 `bash` 執行 `ls -la` 或 `find . -name` 命令查找正確的目錄結構和檔案名。"
		} else if strings.Contains(lowerError, "permission denied") {
			hint = "你沒有權限操作該檔案。請檢查工作區限制，或者思考是否需要修改其他檔案。"
		}
	case "bash":
		if strings.Contains(lowerError, "command not found") {
			hint = "系統中未安裝該命令。請先思考：是否有替代命令？或者你需要先撰寫腳本進行安裝？"
		} else if strings.Contains(rawError, "超時") || strings.Contains(rawError, "DeadlineExceeded") {
			// Match the error from the hand-written 30s context.WithTimeout.
			hint = "該命令執行被超時強殺。如果它是一個常駐服務（如 server 或 watch），請將其轉入後台執行（例如使用 `nohup ... &`），不要阻塞主線程。"
		} else if strings.Contains(lowerError, "syntax error") {
			hint = "Bash 語法錯誤。請檢查引號跳脫或特殊字元，確保命令在終端中可直接執行。"
		}
	}

	if hint == "" {
		return rawError
	}
	return fmt.Sprintf("%s\n\n[系統救援指南]: %s", rawError, hint)
}
