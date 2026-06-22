package utils

import (
	"path/filepath"
	"testing"
)

// assertResolves checks that ResolvePath accepts input and returns want.
func assertResolves(t *testing.T, root, input, want string) {
	t.Helper()
	got, err := ResolvePath(root, input)
	if err != nil {
		t.Fatalf("ResolvePath(%q, %q) unexpected error: %v", root, input, err)
	}
	if got != want {
		t.Fatalf("ResolvePath(%q, %q) = %q, want %q", root, input, got, want)
	}
}

// assertRejects checks that ResolvePath refuses input (path escapes the sandbox).
func assertRejects(t *testing.T, root, input string) {
	t.Helper()
	if got, err := ResolvePath(root, input); err == nil {
		t.Fatalf("ResolvePath(%q, %q) = %q, want error", root, input, got)
	}
}

func TestResolvePath_RelativeInputsStayInside(t *testing.T) {
	root := filepath.Clean("/workspace")

	t.Run("simple file", func(t *testing.T) {
		assertResolves(t, root, "main.go", filepath.Join(root, "main.go"))
	})
	t.Run("nested file", func(t *testing.T) {
		assertResolves(t, root, "internal/utils/path.go", filepath.Join(root, "internal/utils/path.go"))
	})
	t.Run("internal dotdot that stays inside", func(t *testing.T) {
		assertResolves(t, root, "a/../b.go", filepath.Join(root, "b.go"))
	})
	t.Run("dot resolves to root", func(t *testing.T) {
		assertResolves(t, root, ".", root)
	})
}

// TestResolvePath_AbsoluteInputsInsideRoot covers the doubling case from the
// ResolvePath doc comment: an absolute input already under root is taken as-is,
// not concatenated onto root again.
func TestResolvePath_AbsoluteInputsInsideRoot(t *testing.T) {
	root := filepath.Clean("/workspace")

	t.Run("absolute path inside root (no doubling)", func(t *testing.T) {
		assertResolves(t, root, filepath.Join(root, "x/y.go"), filepath.Join(root, "x/y.go"))
	})
	t.Run("absolute path equal to root", func(t *testing.T) {
		assertResolves(t, root, root, root)
	})
}

func TestResolvePath_TraversalIsRejected(t *testing.T) {
	root := filepath.Clean("/workspace")

	t.Run("relative traversal escapes root", func(t *testing.T) {
		assertRejects(t, root, "../../etc/passwd")
	})
	t.Run("absolute path outside root", func(t *testing.T) {
		assertRejects(t, root, "/etc/passwd")
	})
	t.Run("absolute traversal climbs out of root", func(t *testing.T) {
		assertRejects(t, root, filepath.Join(root, "../etc/passwd"))
	})
}

// TestResolvePath_SiblingPrefixIsRejected guards the root+separator check: a
// sibling dir like "/workspace-evil" shares the "/workspace" prefix but must
// not pass a naive HasPrefix check.
func TestResolvePath_SiblingPrefixIsRejected(t *testing.T) {
	root := filepath.Clean("/workspace")
	assertRejects(t, root, "/workspace-evil/secret")
}

// TestResolvePath_RelativeRoot verifies behaviour when workDir itself is
// relative: relative inputs join cleanly, but an absolute input can never live
// beneath a relative root, so it is rejected.
func TestResolvePath_RelativeRoot(t *testing.T) {
	root := "workspace"

	assertResolves(t, root, "file.txt", filepath.Join(filepath.Clean(root), "file.txt"))
	assertRejects(t, root, "/etc/passwd")
}
