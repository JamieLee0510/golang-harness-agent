// Package agentctx holds context keys shared across layers.
// It is deliberately kept minimal (importing only context) so that the lower-level tools package and the upper-level telegram package
// can both import it without creating an import cycle (telegram → engine → tools → agentctx).
package agentctx

import "context"

// reporterKey is the unique key for the reporter in the context. A private type avoids collisions with keys from other packages.
type reporterKey struct{}

// WithReporter injects the current session's reporter into the context.
// The type is deliberately any: the lower-level tools package doesn't know engine.Reporter / telegram.TelegramReporter,
// so the layer that actually uses it (engine.RunSub) performs the type assertion.
func WithReporter(ctx context.Context, reporter any) context.Context {
	return context.WithValue(ctx, reporterKey{}, reporter)
}

// ReporterFromCtx extracts the reporter; returns nil when not present in the context.
func ReporterFromCtx(ctx context.Context) any {
	return ctx.Value(reporterKey{})
}
