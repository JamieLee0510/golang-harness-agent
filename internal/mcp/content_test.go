package mcp

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFlattenContent(t *testing.T) {
	t.Run("single text block", func(t *testing.T) {
		got := flattenContent([]mcp.Content{&mcp.TextContent{Text: "hello"}})
		if got != "hello" {
			t.Fatalf("got %q, want %q", got, "hello")
		}
	})

	t.Run("multiple text blocks joined by newline", func(t *testing.T) {
		got := flattenContent([]mcp.Content{
			&mcp.TextContent{Text: "line1"},
			&mcp.TextContent{Text: "line2"},
		})
		if got != "line1\nline2" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("non-text becomes placeholder, not base64", func(t *testing.T) {
		got := flattenContent([]mcp.Content{
			&mcp.TextContent{Text: "caption"},
			&mcp.ImageContent{MIMEType: "image/png", Data: []byte("rawbytes")},
		})
		if !strings.Contains(got, "caption") {
			t.Fatalf("text dropped: %q", got)
		}
		if !strings.Contains(got, "[image content omitted") || !strings.Contains(got, "image/png") {
			t.Fatalf("image not placeholdered: %q", got)
		}
		if strings.Contains(got, "rawbytes") {
			t.Fatalf("raw image bytes leaked into output: %q", got)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		if got := flattenContent(nil); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}
