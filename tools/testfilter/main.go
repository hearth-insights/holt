
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// TestEvent corresponds to the structure of the JSON objects output by `go test -json`.
type TestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test,omitempty"`
	Output  string `json:"Output,omitempty"`
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)

	// testOutput stores the complete, buffered output for each test.
	testOutput := make(map[string]*strings.Builder)
	// testFailed stores the names of tests that have failed.
	testFailed := make(map[string]bool)
	// packageSummaries stores the final summary line for each package.
	packageSummaries := make(map[string]string)

	for scanner.Scan() {
		line := scanner.Text()
		var event TestEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue // Ignore non-JSON lines
		}

		// Create a unique key for sub-tests, otherwise just use the package name.
		key := event.Package
		if event.Test != "" {
			key = fmt.Sprintf("%s/%s", event.Package, event.Test)
		}

		// Buffer all output lines.
		if event.Action == "output" {
			if _, ok := testOutput[key]; !ok {
				testOutput[key] = &strings.Builder{}
			}
			testOutput[key].WriteString(event.Output)
		}

		// Record the final status of tests and packages.
		if event.Action == "fail" {
			testFailed[key] = true
		}

		// Capture the final summary line for the package.
		if event.Action == "output" && (strings.HasPrefix(event.Output, "FAIL	") || strings.HasPrefix(event.Output, "ok	")) {
			packageSummaries[event.Package] = strings.TrimSpace(event.Output)
		}
	}

	anyFailures := false
	// After processing all input, print the results.
	for key := range testFailed {
		anyFailures = true
		if buffer, ok := testOutput[key]; ok {
			fmt.Fprint(w, buffer.String())
		}
	}

	// Print the summary lines for packages that contained failures.
	for _, summary := range packageSummaries {
		if strings.HasPrefix(summary, "FAIL") {
			anyFailures = true
			fmt.Fprintln(w, summary)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "error reading stdin:", err)
		return err
	}

	if anyFailures {
		return fmt.Errorf("test failures found")
	}
	return nil
}
