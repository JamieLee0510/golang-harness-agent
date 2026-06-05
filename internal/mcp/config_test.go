package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTemp writes content to a temp mcp.json and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadConfig_MissingFileIsEmptyNotError(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if cfg == nil || len(cfg.Servers) != 0 {
		t.Fatalf("missing file should yield empty config, got: %+v", cfg)
	}
}

func TestLoadConfig_MalformedIsError(t *testing.T) {
	path := writeTemp(t, `{ this is not json `)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("malformed json should error")
	}
}

func TestLoadConfig_StdioServer(t *testing.T) {
	path := writeTemp(t, `{
      "mcpServers": {
        "filesystem": {
          "transport": "stdio",
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"],
          "env": ["SOME_TOKEN"]
        }
      }
    }`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv, ok := cfg.Servers["filesystem"]
	if !ok {
		t.Fatal("filesystem server not parsed")
	}
	if srv.Command != "npx" || len(srv.Args) != 3 || srv.Args[0] != "-y" {
		t.Fatalf("stdio fields not parsed correctly: %+v", srv)
	}
	if len(srv.Env) != 1 || srv.Env[0] != "SOME_TOKEN" {
		t.Fatalf("env name list not parsed: %+v", srv.Env)
	}
}

func TestLoadConfig_HTTPServerExpandsEnv(t *testing.T) {
	t.Setenv("API_TOKEN", "secret123")
	t.Setenv("MCP_HOST", "mcp.example.com")

	path := writeTemp(t, `{
      "mcpServers": {
        "remote": {
          "transport": "http",
          "url": "https://${MCP_HOST}/sse",
          "headers": { "Authorization": "Bearer ${API_TOKEN}" }
        }
      }
    }`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv := cfg.Servers["remote"]
	if srv.URL != "https://mcp.example.com/sse" {
		t.Fatalf("URL ${VAR} not expanded: %q", srv.URL)
	}
	if got := srv.Headers["Authorization"]; got != "Bearer secret123" {
		t.Fatalf("header ${VAR} not expanded: %q", got)
	}
}

func TestLoadConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{"stdio missing command", `{"mcpServers":{"x":{"transport":"stdio"}}}`, true},
		{"http missing url", `{"mcpServers":{"x":{"transport":"http"}}}`, true},
		{"missing transport", `{"mcpServers":{"x":{"command":"foo"}}}`, true},
		{"unknown transport", `{"mcpServers":{"x":{"transport":"carrier-pigeon"}}}`, true},
		{"valid stdio", `{"mcpServers":{"x":{"transport":"stdio","command":"foo"}}}`, false},
		{"valid http", `{"mcpServers":{"x":{"transport":"http","url":"https://h/sse"}}}`, false},
		{"empty servers map", `{"mcpServers":{}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfig(writeTemp(t, tt.json))
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
