// Package agentctx 存放跨層共享的 context 鍵。
// 它刻意維持極簡（只 import context），讓底層的 tools 套件與上層的 telegram 套件
// 都能 import 它而不會造成 import 循環（telegram → engine → tools → agentctx）。
package agentctx

import "context"

// reporterKey 是 reporter 在 context 裡的唯一鍵。私有型別避免與其他套件的 key 碰撞。
type reporterKey struct{}

// WithReporter 把當前會話的 reporter 塞進 context。
// 型別刻意用 any：底層 tools 套件不認識 engine.Reporter / telegram.TelegramReporter，
// 由真正使用它的那一層（engine.RunSub）再做型別斷言。
func WithReporter(ctx context.Context, reporter any) context.Context {
	return context.WithValue(ctx, reporterKey{}, reporter)
}

// ReporterFromCtx 取出 reporter；context 裡沒有時回傳 nil。
func ReporterFromCtx(ctx context.Context) any {
	return ctx.Value(reporterKey{})
}
