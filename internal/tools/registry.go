package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// MiddlewareFunc 是工具執行前的攔截器簽名。
// 回傳 allowed 表示是否放行，rejectReason 為攔截時要回傳給模型的原因。
type MiddlewareFunc func(ctx context.Context, call schema.ToolCall) (allowed bool, rejectReason string)

// BaseTool 是所有工具都需實作的通用介面。
type BaseTool interface {
	// Name 回傳全域唯一的工具名（模型以此名稱呼叫工具）。
	Name() string

	// Definition 回傳提交給模型的元資料與參數 JSON schema。
	Definition() schema.ToolDefinition

	// Execute 接收模型輸出的 JSON 參數，執行實際的功能邏輯。
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry 管理工具的註冊、中間件掛載與路由執行。
type Registry interface {
	// Register 向系統掛載一個新工具。
	Register(tool BaseTool)

	// Use 掛載一個全域中間件。
	Use(mw MiddlewareFunc)

	// GetAvailableTools 回傳所有已掛載工具的 schema，供主迴圈提交給 Provider。
	GetAvailableTools() []schema.ToolDefinition

	// Execute 路由並執行模型請求的工具呼叫。
	Execute(ctx context.Context, call schema.ToolCall) schema.ToolResult
}

// registryImpl 是 Registry 的預設實作。
type registryImpl struct {
	// tools 以工具名為鍵，提供 O(1) 路由查找。
	tools       map[string]BaseTool
	middlewares []MiddlewareFunc
}

// NewRegistry 建立一個空的 Registry。
func NewRegistry() Registry {
	return &registryImpl{
		tools:       make(map[string]BaseTool),
		middlewares: make([]MiddlewareFunc, 0),
	}
}

func (r *registryImpl) Register(tool BaseTool) {
	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		log.Printf("[Warning] tool %s has been already registered; will be overwritten.\n", name)
	}
	r.tools[name] = tool
	log.Printf("[Registry] mount tool successfully: %s\n", name)
}

func (r *registryImpl) Use(mw MiddlewareFunc) {
	r.middlewares = append(r.middlewares, mw)
}

func (r *registryImpl) GetAvailableTools() []schema.ToolDefinition {
	var defs []schema.ToolDefinition
	for _, tool := range r.tools {
		defs = append(defs, tool.Definition())
	}
	return defs
}

func (r *registryImpl) Execute(ctx context.Context, call schema.ToolCall) schema.ToolResult {
	// 1. 路由查找；找不到通常代表模型幻覺出不存在的工具。
	tool, exists := r.tools[call.Name]
	if !exists {
		errMsg := fmt.Sprintf("Error: the tool '%s' doesn't exist in system", call.Name)
		return schema.ToolResult{
			ToolCallId: call.ID,
			Output:     errMsg,
			IsError:    true,
		}
	}

	// 2. 執行工具前，依序通過所有中間件做安全把關。
	for _, mw := range r.middlewares {
		allowed, rejectReason := mw(ctx, call)
		if !allowed {
			log.Printf("[Registry] ⚠️ 工具 %s 被 Middleware 攔截: %s\n", call.Name, rejectReason)
			return schema.ToolResult{
				ToolCallId: call.ID,
				Output:     fmt.Sprintf("執行被系統攔截。原因: %s", rejectReason),
				IsError:    true,
			}
		}
	}

	// 3. 執行工具。
	output, err := tool.Execute(ctx, call.Arguments)

	// 4. 封裝結果回傳給主迴圈。
	if err != nil {
		errMsg := fmt.Sprintf("Error executing %s: %v", call.Name, err)
		return schema.ToolResult{ToolCallId: call.ID, Output: errMsg, IsError: true}
	}

	return schema.ToolResult{
		ToolCallId: call.ID,
		Output:     output,
		IsError:    false,
	}
}
