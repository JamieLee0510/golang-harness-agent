package mcp

import (
	"context"
	"encoding/json"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// nameSeparator joins a server name and a remote tool name into the public tool
// name exposed to the model, e.g. "filesystem__read_file". This namespacing
// prevents collisions between servers, and between MCP tools and local tools
// (the official filesystem server, for instance, also exposes read_file /
// write_file / edit_file).
const nameSeparator = "__"

// mcpToolAdapter wraps a single remote MCP tool as a tools.BaseTool, so the
// existing registry / loop / provider can drive it with zero changes.
type mcpToolAdapter struct {
	session    *mcp.ClientSession    // live connection to the owning server
	remoteName string                // the tool's original name ON the server
	def        schema.ToolDefinition // public definition (namespaced name + schema)
}

// newAdapter builds an adapter for one discovered tool under a server's namespace.
func newAdapter(session *mcp.ClientSession, serverName string, t *mcp.Tool) *mcpToolAdapter {
	return &mcpToolAdapter{
		session:    session,
		remoteName: t.Name,
		def: schema.ToolDefinition{
			Name:        serverName + nameSeparator + t.Name,
			Description: t.Description,
			// MCP inputSchema is already JSON Schema; it passes through the
			// provider untouched (see internal/provider/openai.go).
			InputSchema: t.InputSchema,
		},
	}
}

func (a *mcpToolAdapter) Name() string                      { return a.def.Name }
func (a *mcpToolAdapter) Definition() schema.ToolDefinition { return a.def }

// Execute forwards the model's JSON arguments to the server via tools/call and
// returns the flattened text result. ctx is propagated so cancellation and
// timeouts reach the remote call.
func (a *mcpToolAdapter) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	res, err := a.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      a.remoteName, // call the server using its ORIGINAL name
		Arguments: args,         // json.RawMessage marshals back to the same object
	})
	if err != nil {
		return "", err
	}

	out := flattenContent(res.Content)

	// A tool-level error (res.IsError) is not a transport failure: the server
	// ran the tool and it failed. Hand the message back as normal output so the
	// model can read it and self-correct, mirroring how local tools surface
	// recoverable errors.
	return out, nil
}
