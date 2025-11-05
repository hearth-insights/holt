package main

import (
	"bytes"
	"strings"
	"testing"
)

// This sample accurately represents the JSON stream for a mix of passing tests,
// failing tests, and a package-level build failure.
const sampleTestOutput = `
{"Time":"2025-11-01T22:00:00Z","Action":"run","Package":"example.com/pass","Test":"TestPass"}
{"Time":"2025-11-01T22:00:00Z","Action":"output","Package":"example.com/pass","Test":"TestPass","Output":"--- PASS: TestPass (0.00s)\n"}
{"Time":"2025-11-01T22:00:00Z","Action":"pass","Package":"example.com/pass","Test":"TestPass","Elapsed":0}
{"Time":"2025-11-01T22:00:00Z","Action":"output","Package":"example.com/pass","Output":"ok  \texample.com/pass\t0.00s\n"}
{"Time":"2025-11-01T22:00:00Z","Action":"pass","Package":"example.com/pass"}
{"Time":"2025-11-01T22:00:01Z","Action":"run","Package":"example.com/fail","Test":"TestFail"}
{"Time":"2025-11-01T22:00:01Z","Action":"output","Package":"example.com/fail","Test":"TestFail","Output":"=== RUN   TestFail\n"}
{"Time":"2025-11-01T22:00:01Z","Action":"output","Package":"example.com/fail","Test":"TestFail","Output":"    main_test.go:10: This is an error message\n"}
{"Time":"2025-11-01T22:00:01Z","Action":"output","Package":"example.com/fail","Test":"TestFail","Output":"--- FAIL: TestFail (0.01s)\n"}
{"Time":"2025-11-01T22:00:01Z","Action":"fail","Package":"example.com/fail","Test":"TestFail","Elapsed":0.01}
{"Time":"2025-11-01T22:00:01Z","Action":"output","Package":"example.com/fail","Output":"FAIL\t_test/example.com/fail\t0.01s\n"}
{"Time":"2025-11-01T22:00:01Z","Action":"fail","Package":"example.com/fail"}
{"Time":"2025-11-01T22:00:02Z","Action":"output","Package":"example.com/buildfail","Output":"# example.com/buildfail\n"}
{"Time":"2025-11-01T22:00:02Z","Action":"output","Package":"example.com/buildfail","Output":"./fail_test.go:5:1: syntax error\n"}
{"Time":"2025-11-01T22:00:02Z","Action":"output","Package":"example.com/buildfail","Output":"FAIL\t_test/example.com/buildfail [build failed]\n"}
{"Time":"2025-11-01T22:00:02Z","Action":"fail","Package":"example.com/buildfail"}
`

func TestJSONParser(t *testing.T) {
	input := strings.NewReader(sampleTestOutput)
	var output bytes.Buffer

	err := run(input, &output)

	// 1. Should return an error because failures were found
	if err == nil {
		t.Fatal("Expected an error for failing tests, but got nil")
	}

	outputStr := output.String()

	// 2. Should contain the output from the failed test
	if !strings.Contains(outputStr, "=== RUN   TestFail") || !strings.Contains(outputStr, "This is an error message") {
		t.Errorf("Expected output to contain logs from TestFail, but it didn't. Got:\n%s", outputStr)
	}

	// 3. Should contain the package-level build failure output
	if !strings.Contains(outputStr, "./fail_test.go:5:1: syntax error") {
		t.Errorf("Expected output to contain package build failure, but it didn't. Got:\n%s", outputStr)
	}

	// 4. Should contain the final summary lines for failing packages
	if !strings.Contains(outputStr, "FAIL\t_test/example.com/fail") {
		t.Errorf("Expected output to contain summary for failing package, but it didn't. Got:\n%s", outputStr)
	}

	// 5. Should NOT contain output from the passing test or package
	if strings.Contains(outputStr, "TestPass") {
		t.Errorf("Expected output to not contain logs from TestPass, but it did. Got:\n%s", outputStr)
	}
	if strings.Contains(outputStr, "ok  \texample.com/pass") {
		t.Errorf("Expected output to not contain summary for passing package, but it did. Got:\n%s", outputStr)
	}
}