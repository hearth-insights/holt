
package main

import (
	"bytes"
	"strings"
	"testing"
)

const e2eFailureOutput = `
{"Time":"2025-11-09T19:41:05Z","Action":"run","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows"}
{"Time":"2025-11-09T19:41:05Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"=== RUN   TestE2E_Phase2_MultipleWorkflows\n"}
{"Time":"2025-11-09T19:41:05Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e_phase2_test.go:162: === Phase 2 Multiple Workflows Test ===\n"}
{"Time":"2025-11-09T19:41:06Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Detecting instance state...\n"}
{"Time":"2025-11-09T19:41:06Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  ✓ No existing instance found (fresh start)\n"}
{"Time":"2025-11-09T19:41:06Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Docker socket GID: 20 (macOS detected - using GID 0 for container)\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"✓ Started agent container: holt-test-e2e-20251109-194106-000000-GitAgent\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Validating agent health checks...\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  ✓ holt-test-e2e-20251109-194106-000000-GitAgent (healthy)\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  Registered agent 'GitAgent' with image f013a369c997\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"✓ Registered 1 agent image(s) for audit trail\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"✓ \n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Instance 'test-e2e-20251109-194106-000000' started successfully (1 agents ready)\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Workspace: /private/var/folders/x4/skm5m0mn61l3zqq3xptcv6sm0000gn/T/test-e2e-20251109-194106-3665636648\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Next steps:\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  1. Run 'holt forage --goal \"your goal\"' to start a workflow\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  2. Run 'holt logs GitAgent' to view agent logs\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  3. Run 'holt down --name test-e2e-20251109-194106-000000' when finished\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e.go:275: ✓ Container holt-orchestrator-test-e2e-20251109-194106-000000 is running\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e.go:275: ✓ Container holt-test-e2e-20251109-194106-000000-GitAgent is running\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e.go:237: Waiting for Redis to be ready on localhost:6382...\n"}
{"Time":"2025-11-09T19:41:07Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e.go:240: ✓ Redis is ready\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e_phase2_test.go:195: Executing first workflow...\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"✓ Goal artefact created: 573e00f6-bf73-4e8b-8321-0ad6245625bb\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Next steps:\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  • Agents will process this goal in Phase 2+\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  • View all artefacts: holt hoard --name test-e2e-20251109-194106-000000\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  • Monitor workflow: holt watch --name test-e2e-20251109-194106-000000\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e.go:314: Waiting for artefact of type 'CodeCommit'...\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e.go:361: ✗ other artefact: type=GoalDefined, id=573e00f6-bf73-4e8b-8321-0ad6245625bb, v=1 payload=file1.txt\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e.go:358: ✓ Found artefact: type=CodeCommit, id=cd8e0704-b9b9-4a32-bbda-a5348dddc0da, payload=e850a539e758fe333ff15eed8a7c650e420ce849\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e.go:428: ✓ Git commit e850a539e758fe333ff15eed8a7c650e420ce849 exists\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e.go:436: ✓ File file1.txt exists\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e_phase2_test.go:208: ✓ First workflow complete\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e_phase2_test.go:211: Executing second workflow...\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Git workspace is not clean\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Uncommitted changes:\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":" M 1ade6bb9d57c0ba8fc406461ab7becc211a05359\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Either:\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  1. Commit changes:\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  git add .\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  git commit -m \"your message\"\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  2. Stash temporarily:\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"  git stash\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"    e2e_phase2_test.go:214: \n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"                Error Trace:    /Users/cam/github/holt/cmd/holt/commands/e2e_phase2_test.go:214\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"                Error:          Received unexpected error:\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"                                Git workspace is not clean\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"                Test:           TestE2E_Phase2_MultipleWorkflows\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"→ Stopping /holt-test-e2e-20251109-194106-000000-GitAgent...\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"→ Stopping /holt-orchestrator-test-e2e-20251109-194106-000000...\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"→ Stopping /holt-redis-test-e2e-20251109-194106-000000...\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"→ Removing /holt-test-e2e-20251109-194106-000000-GitAgent...\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"→ Removing /holt-orchestrator-test-e2e-20251109-194106-000000...\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"→ Removing /holt-redis-test-e2e-20251109-194106-000000...\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"→ Removing network holt-network-test-e2e-20251109-194106-000000...\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"✓ \n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"Instance 'test-e2e-20251109-194106-000000' removed successfully\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"output","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Output":"--- FAIL: TestE2E_Phase2_MultipleWorkflows (2.85s)\n"}
{"Time":"2025-11-09T19:41:08Z","Action":"fail","Package":"github.com/dyluth/holt/cmd/holt/commands","Test":"TestE2E_Phase2_MultipleWorkflows","Elapsed":2.85}
`

func TestE2E_FailureScenario(t *testing.T) {
	input := strings.NewReader(e2eFailureOutput)
	var output bytes.Buffer

	err := run(input, &output)

	if err == nil {
		t.Fatal("Expected an error for failing tests, but got nil")
	}

	outputStr := output.String()

	// Check that the output contains the key parts of the failure
	expectedError := "Git workspace is not clean"
	if !strings.Contains(outputStr, expectedError) {
		t.Errorf("Expected output to contain the error message '%s', but it didn't. Got:\n%s", expectedError, outputStr)
	}

	// Check that the output contains the test name
	expectedTestName := "TestE2E_Phase2_MultipleWorkflows"
	if !strings.Contains(outputStr, expectedTestName) {
		t.Errorf("Expected output to contain the test name '%s', but it didn't. Got:\n%s", expectedTestName, outputStr)
	}
}
