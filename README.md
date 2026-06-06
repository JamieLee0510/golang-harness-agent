# go-agent-harness

A coding agent harness written from scratch in Go — the ReAct loop, tool
orchestration, context management, and human-in-the-loop controls are all
hand-built, **without any agent framework** (no LangGraph, CrewAI, or AutoGen).

The only third-party dependencies are official API/transport SDKs
(OpenAI, Anthropic, Telegram, the official MCP Go SDK) plus a dotenv loader.
Everything that makes it an *agent* — the reasoning loop, tool routing, memory
compaction, error recovery, the MCP client glue, and the sub-agent sandbox — is
implemented directly on the Go standard library.

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
| **MCP client** | `internal/mcp/` | Connects to external MCP servers (stdio **and** HTTP/SSE), discovers their tools, and adapts each into a `BaseTool` — the ReAct loop, registry and provider need zero changes. |
| **Agent Skills** | `internal/context/skill.go` | Project-local `SKILL.md` files discovered from the launch directory and injected into the system prompt; bundled scripts are referenced via the injected `${SKILL_DIR}`. |
| **Sandbox path safety** | `internal/utils/path.go` | File tools resolve every path against the workspace root and reject traversal / absolute-path escapes (`../../etc/passwd`). |

---

## Architecture

```
        Telegram (per-chat sessions)   ·   CLI one-shot (-p)
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

Beyond the local tools, the `tools.Registry` is also fed by **MCP servers**
(`internal/mcp`): at startup the agent reads `.agent/mcp.json`, connects to each
server (stdio or HTTP/SSE), and registers every remote tool as a namespaced
`BaseTool` (`<server>__<tool>`). Two project-local config sources — `.agent/mcp.json`
and `.agent/skills/*/SKILL.md` — are resolved from the **launch directory**, so
config belongs to the project you run the agent from, not the binary's location.

The `reporter` (per-chat output sink) flows down through `context.Context`
(`internal/agentctx`) so that middleware and the sub-agent can report progress
back to the right Telegram chat — without `tools` ever importing `telegram`
(which would create an import cycle).

---

## Package layout

```
cmd/agent            Entry point (wires provider, registry, middleware, bot, MCP)
internal/engine      ReAct main loop, sessions, sub-agent runner, reminder
internal/tools       Tool registry + middleware, and the tools themselves
internal/mcp         MCP client: config, transport, adapter, manager (lifecycle)
internal/provider    LLMProvider interface + OpenAI / Anthropic implementations
internal/context     Prompt composer, context compactor, error recovery, skills
internal/telegram    Telegram bot front-end, reporter, approval flow
internal/utils       Shared helpers (sandbox-safe path resolution)
internal/agentctx    Neutral context keys shared across layers (breaks cycles)
internal/schema      Shared message / tool-call types
```

### Built-in tools

`read_file`, `write_file`, `edit_file` (4-level fuzzy replace),
`bash`, and `spawn_subagent`. Background-process tools
(`bash_background`, `task_list`, `task_logs`, `task_kill`) are implemented and
ready to register. Any tools exposed by configured **MCP servers** are
registered alongside these under a `<server>__<tool>` namespace.

---

## Quickstart

Requires Go 1.25+.

```bash
# 1. Configure secrets (do NOT commit this file).
cp .env.example .env
#   OPENAI_API_KEY=sk-...
#   TELEGRAM_BOT_TOKEN=...   (from @BotFather, only needed for the bot)

# 2a. Run the Telegram bot (default mode). .env is auto-loaded — no `source`.
go run ./cmd/agent

# 2b. …or run a single task non-interactively and exit (CLI mode).
#     NOTE: flags must come BEFORE the task text (Go flag parsing stops at
#     the first positional arg).
go run ./cmd/agent -p "list the files in the workspace"
```

`.env` in the current directory is loaded automatically at startup (real
environment variables take precedence). The sandbox root defaults to the
current working directory; override it with `-workdir <dir>`.

In Telegram mode, when the agent attempts a dangerous action (`bash rm -rf`,
`sudo`, writing files, …) it sends an approval request and waits for you to
reply `approve <id>` or `reject <id>`. CLI mode (`-p`) runs without the
approval gate.

### MCP servers

Declare MCP servers in `.agent/mcp.json` (resolved relative to the launch
directory; override with `-mcp-config <path>`):

```json
{
  "mcpServers": {
    "playwright": {
      "transport": "stdio",
      "command": "npx",
      "args": ["-y", "@playwright/mcp@latest"]
    },
    "remote": {
      "transport": "http",
      "url": "https://mcp.example.com/sse",
      "headers": { "Authorization": "Bearer ${API_TOKEN}" }
    }
  }
}
```

- `stdio` spawns a subprocess; `http` connects over Streamable HTTP / SSE.
  `${VAR}` in `url`/`headers` is expanded from the environment.
- For stdio servers, `env` lists the **names** of variables to forward into the
  subprocess (`PATH`/`HOME` are always forwarded so the command resolves).
- List the tools a server exposes without starting the agent:

  ```bash
  go run ./cmd/agent -mcp-list
  ```

### Agent Skills

Drop skills under `.agent/skills/<name>/SKILL.md` (also resolved from the launch
directory). Each is parsed for `name` / `description` frontmatter and its body
is injected into the system prompt. A skill may bundle scripts and reference
them with `${SKILL_DIR}`, which is substituted with the skill's absolute
directory at load time:

```markdown
---
name: image-grayscale
description: Use when the user asks to convert an image to black & white.
---
Run the bundled script via the bash tool:
    python3 ${SKILL_DIR}/to_grayscale.py <input> [output]
```

> `.agent/` is git-ignored by default — it holds machine-local config.

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
- MCP discovery is **static**: `mcp.json` is read once at startup; editing it
  requires a restart (no hot-reload / `list_changed` refresh).
- MCP tool descriptions and results are **not yet isolated as untrusted input**
  (tool-poisoning / indirect injection surface — see `.self-note`).
- Skill parsing is line-based frontmatter; the progressive-disclosure and
  conditional-`paths` activation from the design notes are not implemented.
- Test coverage is a work in progress.

## License

MIT (see `LICENSE`).
