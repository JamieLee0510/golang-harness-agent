package mcp

import (
	"context"
	"errors"
	"log"

	"github.com/JamieLee0510/go-agent-harness/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Manager owns the lifecycle of every MCP connection for one agent process.
// It connects to all configured servers, registers their tools (namespaced)
// into the given Registry, and closes every session on shutdown.
//
// It implements tools.Closer so the entry point can treat teardown uniformly.
type Manager struct {
	sessions []*mcp.ClientSession
}

var _ tools.Closer = (*Manager)(nil)

// Start connects to every server in cfg, discovers their tools, and registers
// an adapter per tool into registry. Discovery is STATIC: it happens once here,
// at startup; the tool set is fixed for the process lifetime (design §"發現模型").
//
// Resilience (design §10): a server that fails to connect or list is logged and
// skipped — it never aborts startup, and the remaining servers plus all local
// tools keep working. Start itself returns an error only for a programming
// fault, never for a single server being down.
func Start(ctx context.Context, cfg *Config, registry tools.Registry) (*Manager, error) {
	m := &Manager{}
	if cfg == nil || len(cfg.Servers) == 0 {
		return m, nil // MCP disabled; nothing to do.
	}

	for name, srv := range cfg.Servers {
		log.Printf("[MCP] connecting to %q (%s)...", name, srv.Transport)
		session, err := Connect(ctx, srv)
		if err != nil {
			log.Printf("[MCP] ⚠️ %q connect failed, skipping: %v", name, err)
			continue
		}

		toolList, err := DiscoverTools(ctx, session)
		if err != nil {
			log.Printf("[MCP] ⚠️ %q list tools failed, skipping: %v", name, err)
			_ = session.Close()
			continue
		}

		// Keep the session alive for the process lifetime and register adapters.
		m.sessions = append(m.sessions, session)
		for _, t := range toolList {
			registry.Register(newAdapter(session, name, t))
		}
		log.Printf("[MCP] %q registered %d tool(s)", name, len(toolList))
	}

	return m, nil
}

// Close gracefully closes every live session, joining all errors so one bad
// close does not hide the others.
func (m *Manager) Close() error {
	var errs []error
	for _, s := range m.sessions {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	m.sessions = nil
	return errors.Join(errs...)
}
