# go-agent-harness

A coding agent harness written from scratch in Go — the ReAct loop, tool
orchestration, context management, and human-in-the-loop controls are all
hand-built, **without any agent framework** (no LangGraph, CrewAI, or AutoGen).

The only third-party dependencies are official API/transport SDKs
(OpenAI, Anthropic, Telegram). Everything that makes it an *agent* — the
reasoning loop, tool routing, memory compaction, error recovery, and the
sub-agent sandbox — is implemented directly on the Go standard library.

> **Why build this?** Agent frameworks hide the hard parts behind decorators
> and graph DSLs. This project re-implements those hard parts in ~2.8k lines of
> readable Go to show how an agent loop actually works underneath.

---

## What it demonstrates

Each item below is something a framework would normally give you for free —
here it is hand-written and self-contained:

| Capability | Where | Why it's interesting |
|---|---|---|
| **ReAct loop** | `internal/engine/loop.go` | Reason → Act → Observe cycle with an optional tools-disabled "thinking" phase and a clean termination condition. |
| **Tool registry + middleware** | `internal/tools/registry.go` | O(1) tool routing plus a middleware chain that can intercept any call before execution. |
| **Human-in-the-loop approval** | `internal/telegram/approve.go` | A middleware suspends the calling goroutine on a channel and waits for a human `approve/reject` reply, with timeout and cancellation. |
| **Context compaction** | `internal/context/compactor.go` | Token-window management: early tool outputs are masked, oversized payloads are head-tail truncated, tool-call evidence is preserved. |
| **Error self-recovery** | `internal/context/recovery.go` | Maps tool errors (POSIX errnos, fuzzy-match failures, timeouts) to actionable hints injected back to the model. |
| **Anti-loop reminder** | `internal/engine/reminder.go` | Fingerprints repeated tool calls and force-injects a corrective system reminder after N consecutive identical failures. |
| **Read-only sub-agent** | `internal/tools/subagent.go` | The main agent can delegate exploration to an isolated sub-loop that only gets a read-only tool registry. |
| **Concurrent tool execution** | `internal/engine/loop.go` | Tool calls in a turn run in parallel via goroutines into a pre-allocated slice — no mutex, no TOCTOU on shared state. |
| **Pluggable providers** | `internal/provider/` | A single `LLMProvider` interface backs both OpenAI and Anthropic Claude. |

---

## Architecture

```
                 Telegram (per-chat sessions)
                          │
                          ▼
        ┌─────────────────────────────────────┐
        │            AgentEngine               │   internal/engine
        │  ReAct loop · compaction · recovery  │
        │        · reminder · sub-agent        │
        └───────┬───────────────────┬──────────┘
                │                   │
        Generate│                   │Execute(ctx, call)
                ▼                   ▼
        ┌───────────────┐   ┌──────────────────────┐
        │  LLMProvider  │   │   tools.Registry      │  internal/tools
        │ OpenAI/Claude │   │  middleware → routing │
        └───────────────┘   └───────────┬──────────┘
                                         │ (dangerous call)
                                         ▼
                              ┌────────────────────┐
                              │  ApprovalManager    │  internal/telegram
                              │  block on channel,  │
                              │  await human reply  │
                              └────────────────────┘
```

The `reporter` (per-chat output sink) flows down through `context.Context`
(`internal/agentctx`) so that middleware and the sub-agent can report progress
back to the right Telegram chat — without `tools` ever importing `telegram`
(which would create an import cycle).

---

## Package layout

```
cmd/agent            Entry point (wires provider, registry, middleware, bot)
internal/engine      ReAct main loop, sessions, sub-agent runner, reminder
internal/tools       Tool registry + middleware, and the tools themselves
internal/provider    LLMProvider interface + OpenAI / Anthropic implementations
internal/context     Prompt composer, context compactor, error recovery, skills
internal/telegram    Telegram bot front-end, reporter, approval flow
internal/agentctx    Neutral context keys shared across layers (breaks cycles)
internal/schema      Shared message / tool-call types
```

### Built-in tools

`read_file`, `write_file`, `edit_file` (4-level fuzzy replace),
`bash`, and `spawn_subagent`. Background-process tools
(`bash_background`, `task_list`, `task_logs`, `task_kill`) are implemented and
ready to register.

---

## Quickstart

Requires Go 1.25+.

```bash
# 1. Configure secrets (do NOT commit this file)
cp .env.example .env
#   OPENAI_API_KEY=sk-...
#   TELEGRAM_BOT_TOKEN=...   (from @BotFather)

# 2. Load env and run the Telegram bot
source .env
go run ./cmd/agent
```

Then message your bot on Telegram. Each chat keeps its own session/history.
When the agent attempts a dangerous action (`bash rm -rf`, `sudo`, writing
files, …) it sends an approval request and waits for you to reply
`approve <id>` or `reject <id>`.

The agent operates inside the `./workspace` directory as its sandbox.

---

## Design decisions worth a look

- **Why hand-roll the loop instead of a framework?** Frameworks couple your
  control flow to their graph/agent abstractions. A plain `for` loop over
  Reason→Act→Observe is easier to reason about, debug, and extend (e.g. adding
  the compaction and reminder hooks took a few lines each, exactly where they
  belong).

- **Middleware for safety, not just logging.** `tools.Registry.Execute` runs a
  middleware chain *before* the tool. Rejection returns an `IsError` result so
  the model is forced to read *why* it was blocked. The Telegram approval flow
  is just one such middleware.

- **Reporter via `context.Context`, not a struct field.** Telegram is
  one-bot-many-chats; a shared `bot.reporter` field would race and cross-talk
  between concurrent chats. Threading the per-chat reporter through `ctx` keeps
  it correct under concurrency with zero shared mutable state.

- **Compaction preserves logical integrity.** The compactor never touches
  `ToolCalls` (the model's action evidence) and skips orphan tool-results at the
  head of the window, because the LLM APIs reject discontinuous tool histories
  with a 400.

- **Recovery as architecture, not regex glue.** `recovery.go` documents up front
  that keyword matching on error strings is a fragile pattern; it exists to show
  *where* a production system would slot in stable POSIX errnos or domain error
  codes.

---

## Status & limitations

This is a learning/portfolio project, not production software.

- Sessions are in-memory only (a JSONL persistence hook is stubbed in
  `Session.Append`).
- Error recovery uses keyword matching — intentionally illustrative (see above).
- No retry/back-off on provider calls yet.
- Test coverage is a work in progress.

## License

MIT (see `LICENSE`).
