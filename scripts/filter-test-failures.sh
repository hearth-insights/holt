#!/bin/bash
#
# filter-test-failures.sh
# Filters `go test -json` output to show only failed tests
#
# Usage: go test -json ./... 2>&1 | bash scripts/filter-test-failures.sh

# Use jq to parse JSON and extract failures
# Strategy:
# 1. Group all events by test name
# 2. For each test that has action=="fail", print all its output

# Temporary file to collect test data
tmpfile=$(mktemp)
trap "rm -f $tmpfile" EXIT

# Read all JSON lines, filter for test events, and store
while IFS= read -r line; do
    echo "$line" >> "$tmpfile"
done

# Process the collected data
failed_tests=$(jq -r 'select(.Action == "fail" and .Test != null) | .Package + "::" + .Test' "$tmpfile" 2>/dev/null | sort -u)

if [ -z "$failed_tests" ]; then
    # No test failures, but check for package-level failures (build errors)
    pkg_failures=$(jq -r 'select(.Action == "fail" and .Test == null) | .Package' "$tmpfile" 2>/dev/null | sort -u)

    if [ -n "$pkg_failures" ]; then
        echo "========================================"
        echo "PACKAGE BUILD FAILURES"
        echo "========================================"
        echo ""
        for pkg in $pkg_failures; do
            echo "Package: $pkg"
            jq -r --arg pkg "$pkg" 'select(.Package == $pkg and .Action == "output" and .Test == null) | .Output' "$tmpfile" 2>/dev/null
            echo ""
        done
        exit 1
    fi

    # No failures at all
    exit 0
fi

# Print header
echo "========================================"
echo "FAILED TESTS ($(echo "$failed_tests" | wc -l | tr -d ' ') total)"
echo "========================================"
echo ""

# For each failed test, print its complete output
for test_info in $failed_tests; do
    pkg=$(echo "$test_info" | cut -d: -f1)
    test=$(echo "$test_info" | cut -d: -f3-)

    echo "========================================"
    echo "FAILED: $test"
    echo "Package: $pkg"
    echo "========================================"

    # Print all output for this specific test
    jq -r --arg pkg "$pkg" --arg test "$test" \
        'select(.Package == $pkg and .Test == $test and .Action == "output") | .Output' \
        "$tmpfile" 2>/dev/null

    echo ""
done

exit 1
