#!/bin/sh
# This script intelligently parses Go test output from stdin to show only the
# full log blocks for tests that have failed.

# It uses awk to implement a robust stateful parser:
# 1. When a line indicates a new test is running (`=== RUN`), it clears a
#    single buffer and notes the current test name.
# 2. It then appends every subsequent line to this buffer.
# 3. If the test passes (`--- PASS`), the buffer is simply cleared.
# 4. If the test fails (`--- FAIL`), the entire buffer is printed, and then cleared.
# 5. This ensures that only the complete log context for failed tests is shown.

awk '
# When a new test starts, reset the buffer and note the test name.
/^=== RUN/ {
  buffer = ""
  current_test_name = $NF
}

# Buffer every line that comes after "=== RUN".
# The check for `current_test_name` ensures we only buffer *inside* a test block.
NF > 0 && current_test_name != "" {
  buffer = buffer $0 "\n"
}

# When a test fails, print the buffer and then clear it.
/^--- FAIL:/ {
  # Check if the test name matches to be extra safe.
  if ($3 == current_test_name) {
    printf "%s", buffer
    found_fail = 1
  }
  buffer = ""
  current_test_name = ""
}

# When a test passes, just clear the buffer and name.
/^--- PASS:/ {
  buffer = ""
  current_test_name = ""
}

# Also print the final summary FAIL lines for context.
/^FAIL\t/ { print; found_fail=1 }

END {
  if (found_fail) {
    exit 1
  }
}'
