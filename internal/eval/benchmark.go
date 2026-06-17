package eval

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	ctxpkg "github.com/JamieLee0510/go-agent-harness/internal/context"
	"github.com/JamieLee0510/go-agent-harness/internal/engine"
	"github.com/JamieLee0510/go-agent-harness/internal/observability"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/JamieLee0510/go-agent-harness/internal/tools"

	"github.com/JamieLee0510/go-agent-harness/internal/provider"
)

type TestCase struct {
	ID             string
	Name           string
	SetupScript    string
	TaskPrompt     string
	ValidateScript string
	MaxTurns       int
}

type TestResult struct {
	TestCaseID   string
	Passed       bool
	TotalCostUSD float64
	DurationsMs  int64
	ErrorMsg     string
}

type BenchmarkRunner struct {
	modelName string
}

func NewBenchmarkRunner(model string) *BenchmarkRunner {
	return &BenchmarkRunner{modelName: model}
}

func (b *BenchmarkRunner) RunSuite(ctx context.Context, testcases []TestCase) {
	log.Println("==================================================")
	log.Printf("🚀 starting Harness Benchmark evaluating... | model: %s\n", b.modelName)
	log.Println("==================================================")

	var results []TestResult
	passedCount := 0
	totalCost := 0.0 // float: costs are sub-dollar, so int truncation would round every case down to 0

	for _, tc := range testcases {
		log.Printf("\n>>> ⏳ executing testcase [%s]: %s\n", tc.ID, tc.Name)

		res := b.RunSingleTest(ctx, tc)
		results = append(results, res)

		if res.Passed {
			passedCount++
			log.Printf(">>> ✅ Case [%s] passed! | duration: %dms | cost: $%.6f\n", tc.ID, res.DurationsMs, res.TotalCostUSD)
		} else {
			log.Printf(">>> ❌ Case [%s] failed! | error: %s\n", tc.ID, res.ErrorMsg)
		}
		totalCost += res.TotalCostUSD
	}
	log.Println("\n================ 🏆 Evaluation Report ================")
	log.Printf("Total test cases: %d | Passed: %d | Passed rate: %.2f%%\n", len(testcases), passedCount, float64(passedCount)/float64(len(testcases))*100)
	log.Printf("Total cost: $%.6f\n", totalCost)
	log.Println("==================================================")
}

func (b *BenchmarkRunner) RunSingleTest(ctx context.Context, tc TestCase) TestResult {
	startTime := time.Now()

	workDir, _ := os.Getwd()
	workDir += fmt.Sprintf("/workspace/%s_%d", tc.ID, time.Now().Unix())
	_ = os.MkdirAll(workDir, 0755)

	if tc.SetupScript != "" {
		cmd := exec.Command("bash", "-c", tc.SetupScript)
		cmd.Dir = workDir
		if err := cmd.Run(); err != nil {
			return TestResult{TestCaseID: tc.ID, Passed: false, ErrorMsg: "setup script failed."}
		}
	}

	realProvider := provider.NewOpenAIProvider(b.modelName)
	session := ctxpkg.NewSession(tc.ID, workDir)
	trackedProvider := observability.NewCostTracker(realProvider, b.modelName)

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))
	registry.Register(tools.NewEditFileTool(workDir))

	eng := engine.NewAgentEngine(trackedProvider, registry, false, false)

	session.Append(schema.Message{Role: schema.RoleUser, Content: tc.TaskPrompt})
	err := eng.Run(ctx, session, nil)

	if err != nil {
		return TestResult{TestCaseID: tc.ID, Passed: false, ErrorMsg: fmt.Sprintf("Agent crashed: %v", err)}
	}

	cmd := exec.Command("bash", "-c", tc.ValidateScript)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()

	duration := time.Since(startTime).Milliseconds()

	if err != nil {
		return TestResult{
			TestCaseID:   tc.ID,
			Passed:       false,
			TotalCostUSD: session.TotalCostUSD,
			DurationsMs:  duration,
			ErrorMsg:     fmt.Sprintf("Validate script failed: %s", string(out)),
		}
	}
	return TestResult{
		TestCaseID:   tc.ID,
		Passed:       true,
		TotalCostUSD: session.TotalCostUSD,
		DurationsMs:  duration,
	}

}
