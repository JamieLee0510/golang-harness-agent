package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Identity this client announces to servers during the initialize handshake.
const (
	clientName    = "go-agent-harness"
	clientVersion = "0.1.0"
)

// Connect dials a single MCP server and returns a live, initialized session.
// The caller owns the session and must Close it when done.
func Connect(ctx context.Context, cfg ServerConfig) (*mcp.ClientSession, error) {
	transport, err := newTransport(cfg)
	if err != nil {
		return nil, err
	}

	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: clientVersion}, nil)

	// Connect performs the MCP initialize handshake and capability negotiation;
	// the returned session is ready to use.
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return session, nil
}

// DiscoverTools lists every tool the connected server currently exposes,
// following pagination cursors until the server reports no more pages.
func DiscoverTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	var all []*mcp.Tool
	params := &mcp.ListToolsParams{}
	for {
		res, err := session.ListTools(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("list tools: %w", err)
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		params.Cursor = res.NextCursor
	}
	return all, nil
}
