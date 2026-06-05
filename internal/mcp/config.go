// Package mcp lets the agent act as an MCP (Model Context Protocol) client:
// it connects to external MCP servers, discovers the tools they expose, and
// adapts each remote tool into a tools.BaseTool so the agent can call it like
// any local tool.
//
// This file (step 1) covers only configuration: the on-disk mcp.json schema,
// its loading, ${VAR} environment expansion, and validation. Transport,
// client, adapter and lifecycle land in later steps.
package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

// Transport kinds supported by a server entry.
const (
	TransportStdio = "stdio" // spawn a subprocess, talk over its stdin/stdout
	TransportHTTP  = "http"  // connect to a remote server over HTTP/SSE
)

// ServerConfig describes a single MCP server the agent should connect to.
//
// For stdio servers, Command/Args/Env are used. For http servers, URL/Headers
// are used. Env lists the *names* of environment variables to forward into the
// subprocess (values are read from this process's environment at launch), so
// secrets never live in mcp.json itself. URL and Header values may contain
// ${VAR} placeholders, expanded from the environment at load time.
type ServerConfig struct {
	Transport string `json:"transport"` // "stdio" | "http"

	// stdio
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"` // env var NAMES to forward (not values)

	// http
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Config is the parsed contents of mcp.json.
type Config struct {
	Servers map[string]ServerConfig `json:"mcpServers"`
}

// LoadConfig reads and validates mcp.json at path.
//
// A missing file is NOT an error: it returns an empty Config so the whole MCP
// subsystem stays silently disabled and the agent runs exactly as before.
// A present-but-malformed file IS an error, because that signals a real
// misconfiguration the user should see.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Servers: map[string]ServerConfig{}}, nil
		}
		return nil, fmt.Errorf("read mcp config %q: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse mcp config %q: %w", path, err)
	}
	if cfg.Servers == nil {
		cfg.Servers = map[string]ServerConfig{}
	}

	// Expand ${VAR} placeholders, then validate each server entry.
	for name, srv := range cfg.Servers {
		srv.expandEnv()
		if err := srv.validate(name); err != nil {
			return nil, err
		}
		cfg.Servers[name] = srv
	}

	return &cfg, nil
}

// expandEnv replaces ${VAR} / $VAR placeholders in URL and Header values using
// the current environment. Only http-relevant fields are expanded; stdio Env
// is a name list and must not be expanded.
func (s *ServerConfig) expandEnv() {
	s.URL = os.ExpandEnv(s.URL)
	if s.Headers != nil {
		expanded := make(map[string]string, len(s.Headers))
		for k, v := range s.Headers {
			expanded[k] = os.ExpandEnv(v)
		}
		s.Headers = expanded
	}
}

// validate checks that a server entry has the fields its transport requires.
func (s *ServerConfig) validate(name string) error {
	switch s.Transport {
	case TransportStdio:
		if s.Command == "" {
			return fmt.Errorf("mcp server %q: stdio transport requires \"command\"", name)
		}
	case TransportHTTP:
		if s.URL == "" {
			return fmt.Errorf("mcp server %q: http transport requires \"url\"", name)
		}
	case "":
		return fmt.Errorf("mcp server %q: missing \"transport\" (want %q or %q)", name, TransportStdio, TransportHTTP)
	default:
		return fmt.Errorf("mcp server %q: unknown transport %q (want %q or %q)", name, s.Transport, TransportStdio, TransportHTTP)
	}
	return nil
}
