package context

import (
	"fmt"
	"strings"
)

// 為求極簡直觀，此處採用基於 strings.Contains 的關鍵字匹配。
//
// 必須說明：在生產環境中，依賴模糊的中文報錯字串做邏輯分支是極脆弱（flaky）的反模式——
// 底層工具的報錯只要改一個字，整個自癒機制就會失效。工業級做法是讓底層工具拋出穩定的
// POSIX 標準錯誤（如 no such file or directory、permission denied），或在 Tool Registry
// 層定義一套領域錯誤碼（如 ERR_FILE_NOT_FOUND、ERR_EDIT_FUZZY_MATCH_FAILED），
// 讓模型同時收到錯誤文字與錯誤碼。此處僅示範 Harness 在架構層如何攔截與注入，
// 企業級落地時請改造為基於標準化 Error Code 的 switch-case。

// RecoveryManager 在工具執行出錯時，依錯誤特徵注入給模型的修正建議。
type RecoveryManager struct{}

// NewRevocerManager 建立 RecoveryManager。
func NewRevocerManager() *RecoveryManager {
	return &RecoveryManager{}
}

// AnalyzeAndInject 比對 toolName 與 rawError 的錯誤特徵，命中時在原始報錯後附上修正建議；
// 未命中則原樣返回 rawError。
func (rm *RecoveryManager) AnalyzeAndInject(toolName string, rawError string) string {
	var hint string

	lowerError := strings.ToLower(rawError)

	switch toolName {
	case "edit_file":
		// 比對 fuzzyReplace 拋出的固定報錯。
		if strings.Contains(rawError, "在檔案中未找到 old_text") || strings.Contains(rawError, "找不到該程式碼片段") {
			hint = "你提供的 old_text 與檔案當前內容不一致，或者缺少必要的縮排。請先使用 `read_file` 工具重新讀取該檔案，取得最新、準確的內容後，再重新發起編輯。"
		} else if strings.Contains(rawError, "匹配到了多處") || strings.Contains(rawError, "提供更多上下文") {
			hint = "你的 old_text 不夠具體，命中了多個相同程式碼塊。請在 old_text 中增加上下相鄰的幾行程式碼，以確保替換的唯一性。"
		}
	case "read_file", "write_file":
		// 比對 Go os 套件拋出的 POSIX 標準錯誤。
		if strings.Contains(lowerError, "no such file or directory") {
			hint = "路徑似乎不正確。請不要憑空猜測，先使用 `bash` 執行 `ls -la` 或 `find . -name` 命令查找正確的目錄結構和檔案名。"
		} else if strings.Contains(lowerError, "permission denied") {
			hint = "你沒有權限操作該檔案。請檢查工作區限制，或者思考是否需要修改其他檔案。"
		}
	case "bash":
		if strings.Contains(lowerError, "command not found") {
			hint = "系統中未安裝該命令。請先思考：是否有替代命令？或者你需要先撰寫腳本進行安裝？"
		} else if strings.Contains(rawError, "超時") || strings.Contains(rawError, "DeadlineExceeded") {
			// 比對手寫的 30s context.WithTimeout 報錯。
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
