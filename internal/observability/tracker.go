package observability

import (
	"context"
	"log"
	"time"

	ctxpkg "github.com/JamieLee0510/go-agent-harness/internal/context"
	"github.com/JamieLee0510/go-agent-harness/internal/provider"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// Currently hardcode LLM pricing (Openai gpt-5-nano)
var PricingModel = map[string]struct {
	InputPrice  float64
	OutputPrice float64
}{
	"gpt-5-nano": {InputPrice: 0.2, OutputPrice: 1.25},
}

// CostTracker decorates an LLMProvider to time each call, price its token usage, and bill it to the
// session of the current request. It holds no session itself: the session is read from ctx at call time
// (bound by engine.Run via ctxpkg.WithSession), so a single shared tracker correctly attributes cost
// across many concurrent sessions (e.g. one per Telegram chat).
type CostTracker struct {
	nextProvider provider.LLMProvider
	modelName    string
}

func NewCostTracker(next provider.LLMProvider, modelName string) *CostTracker {
	return &CostTracker{
		nextProvider: next,
		modelName:    modelName,
	}
}

func (t *CostTracker) Generate(ctx context.Context, msgs []schema.Message, availableTools []schema.ToolDefinition) (*schema.Message, error) {
	startTime := time.Now()

	respMsg, err := t.nextProvider.Generate(ctx, msgs, availableTools)

	latency := time.Since(startTime)

	if err != nil {
		log.Printf("[Tracker]: ❌ API failed, cost time %v\n", latency)
		return respMsg, err
	}

	if respMsg.Usage != nil {
		promptTokens := respMsg.Usage.PromptTokens
		completionTokens := respMsg.Usage.CompletionTokens

		var cost float64
		if price, exists := PricingModel[t.modelName]; exists {
			// Prices in PricingModel are per 1,000,000 tokens, so divide by 1e6 to get the dollar cost of this call.
			cost = (float64(promptTokens)*price.InputPrice + float64(completionTokens)*price.OutputPrice) / 1_000_000
		}
		log.Printf("[Tracker] 📊 API finished | cost time: %v | input: %d tk | output: %d tk | cost: $%.6f\n", latency, promptTokens, completionTokens, cost)

		// Bill this call to the request's session, if one is bound to ctx. Subagent inference (RunSub) inherits
		// the same ctx, so its tokens roll up into the same session bill.
		if session := ctxpkg.SessionFromCtx(ctx); session != nil {
			session.RecordUsage(promptTokens, completionTokens, cost)
			log.Printf("[Tracker] 📊 current session [%s] total cost $%.6f\n", session.ID, session.TotalCostUSD)
		}
	} else {
		log.Printf("[Tracker] ⚠️ API finished but returned no Usage data | cost time: %v\n", latency)
	}

	return respMsg, nil
}
