package mcp

import (
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// flattenContent collapses a CallTool result's content blocks into the single
// string that tools.ToolResult.Output expects.
//
// First-pass policy (design §8): text blocks are concatenated as-is; non-text
// blocks (image / audio / anything else) are replaced by a short placeholder
// rather than dumping base64 into the model's context. Multi-modal passthrough
// would require widening ToolResult and is out of scope here.
func flattenContent(blocks []mcp.Content) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		switch c := b.(type) {
		case *mcp.TextContent:
			parts = append(parts, c.Text)
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[image content omitted: %s, %d bytes]", c.MIMEType, len(c.Data)))
		case *mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio content omitted: %s, %d bytes]", c.MIMEType, len(c.Data)))
		default:
			parts = append(parts, fmt.Sprintf("[unsupported content type %T omitted]", b))
		}
	}
	return strings.Join(parts, "\n")
}
