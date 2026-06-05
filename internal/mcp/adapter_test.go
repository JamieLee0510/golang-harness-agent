package mcp

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewAdapter_Namespacing(t *testing.T) {
	schemaIn := map[string]any{"type": "object"}
	// session is nil: Name()/Definition() must not touch it (only Execute does).
	a := newAdapter(nil, "filesystem", &mcp.Tool{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: schemaIn,
	})

	if a.Name() != "filesystem__read_file" {
		t.Fatalf("public name = %q, want %q", a.Name(), "filesystem__read_file")
	}
	if a.remoteName != "read_file" {
		t.Fatalf("remoteName = %q, want original %q", a.remoteName, "read_file")
	}

	def := a.Definition()
	if def.Name != "filesystem__read_file" {
		t.Fatalf("def.Name = %q", def.Name)
	}
	if def.Description != "Read a file" {
		t.Fatalf("def.Description = %q", def.Description)
	}
	if def.InputSchema == nil {
		t.Fatal("inputSchema should pass through, got nil")
	}
}
