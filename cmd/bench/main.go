package main

import (
	"context"
	"log"
	"os"

	"github.com/JamieLee0510/go-agent-harness/internal/eval"
)

func main() {
	// RunSingleTest builds an OpenAI provider, so the bench needs OPENAI_API_KEY.
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("please export OPENAI_API_KEY before running the benchmark")
	}

	// A tiny evaluation suite. Each case sets up a target workspace, hands the Agent a task,
	// then grades it with a validate script (non-zero exit = fail).
	testcases := []eval.TestCase{
		{
			ID:   "test_001_edit",
			Name: "fuzzy edit_file accuracy",
			// Target: a config.json with a version to bump.
			SetupScript: `echo '{"name": "tiny-claw", "version": "v1.0.0"}' > config.json`,
			// Task: bump the version, nothing else.
			TaskPrompt: `當前目錄下有一個 config.json。請你使用 edit_file 工具，將其中的 version 從 v1.0.0 改為 v2.0.0。不要做其他多餘操作。`,
			// Grade: grep for the new version string.
			ValidateScript: `grep '"version": "v2.0.0"' config.json`,
			MaxTurns:       8,
		},
		{
			ID:   "test_002_code_gen",
			Name: "read code and create a new test file",
			// Target: a simple multiply function. printf (not echo) so the \n escapes expand on macOS bash.
			SetupScript: `printf 'package math\n\nfunc Multiply(a, b int) int {\n\treturn a * b\n}\n' > math.go`,
			// Task: read math.go, then write a proper unit test for Multiply.
			TaskPrompt: `當前目錄下有一個 math.go。請你仔細閱讀它，然後在同級目錄下，幫我寫一個規範的單元測試檔 math_test.go，用來測試 Multiply 函式。請務必包含正常的測試用例。`,
			// Grade: actually run go test. If it doesn't compile or fails, the case scores 0.
			ValidateScript: `go mod init bench >/dev/null 2>&1; go test -v ./...`,
			MaxTurns:       12,
		},
	}

	// gpt-5-nano: cheap and present in the tracker's pricing table. Override with MODEL_NAME if set.
	model := os.Getenv("MODEL_NAME")
	if model == "" {
		model = "gpt-5-nano"
	}

	runner := eval.NewBenchmarkRunner(model)
	runner.RunSuite(context.Background(), testcases)
}
