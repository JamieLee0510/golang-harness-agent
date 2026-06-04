package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvePath safely resolves a user-supplied path (which may be relative or
// absolute) against the sandbox root workDir, and guarantees the result never
// escapes workDir.
//
// Why this exists instead of a bare filepath.Join(workDir, path):
//
//   - filepath.Join assumes path is always relative. Given an ABSOLUTE path it
//     does NOT reset to root (unlike shell `cd`); it just concatenates, so
//     Join("/workspace", "/workspace/x") == "/workspace/workspace/x" — the root
//     gets doubled and the file is not found.
//   - filepath.Join does nothing about traversal: Join("/workspace", "../../etc/passwd")
//     happily yields "/etc/passwd", escaping the sandbox.
//
// ResolvePath fixes both: absolute inputs are taken as-is (after Clean),
// relative inputs are joined onto workDir, and the final path is prefix-checked
// against workDir so anything outside the sandbox is rejected.
func ResolvePath(workDir, p string) (string, error) {
	root := filepath.Clean(workDir)

	var full string
	if filepath.IsAbs(p) {
		full = filepath.Clean(p)
	} else {
		full = filepath.Join(root, p)
	}

	// The resolved path must be the root itself or live strictly beneath it.
	// Appending the separator prevents a sibling like "/workspace-evil" from
	// passing a naive HasPrefix("/workspace") check.
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the workspace %q", p, root)
	}

	return full, nil
}
