# Agent Logging Guide (M4.10)

**Purpose**: Comprehensive guide to the FD 3 Return architecture for agent logging and result output.

**Audience**: Agent developers building agents for Holt.

---

## Overview

Starting with M4.10, Holt uses the **FD 3 Return architecture** to separate agent logs from result data. This allows agents to be "noisy" (run verbose tools, print debug output) without corrupting the JSON result that the orchestrator expects.

### The Problem We Solved

**Before M4.10**, agents had to write their final JSON result to stdout, which meant:
- All tool output had to be silenced (`npm install > /dev/null 2>&1`)
- Debug prints would corrupt the JSON response
- Agents couldn't see what their tools were doing

**With M4.10**, agents use separate channels:
- **FD 3** (file descriptor 3) for the final JSON result
- **stdout/stderr** for logs, tool output, debug prints (anything!)

---

## The FD 3 Return Model

### Channel Definitions

| FD | Channel | Purpose | Handled By |
|----|---------|---------|------------|
| **0** | stdin | JSON input from pup → agent | Pup writes, agent reads |
| **1** | stdout | **Logs, tool output, progress** | Agent writes, Docker captures |
| **2** | stderr | **Errors, warnings, stack traces** | Agent writes, Docker captures |
| **3** | **NEW** | **Final JSON result (ONLY)** | Agent writes, pup reads |

### Key Points

✅ **Stdout/stderr are for humans** - Write anything you want!
✅ **FD 3 is for machines** - Write clean JSON result only
✅ **Logs go to Docker** - View with `holt logs <agent-role>`
✅ **Tools can be noisy** - No more output silencing needed

---

## Quick Start Examples

### Shell Script (Bash/sh)

```bash
#!/bin/sh
# Read input from stdin
input=$(cat)

# Be noisy! All this goes to docker logs (visible via `holt logs`)
echo "Starting work..."
npm install          # Full output visible
git fetch --all      # Full output visible
echo "Processing data..."

# Extract data from input
goal=$(echo "$input" | jq -r '.target_artefact.payload')

# Do work
echo "Working on: $goal"
result=$(./do-work.sh "$goal")

# Return result via FD 3
cat <<EOF >&3
{
  "artefact_type": "WorkComplete",
  "artefact_payload": "$result",
  "summary": "Completed work for $goal"
}
EOF
```

### Python

```python
#!/usr/bin/env python3
import json
import sys
import os
import subprocess

# Read input from stdin
input_data = json.load(sys.stdin)

# Be noisy! Print to stdout/stderr
print("Python agent starting...")
print(f"Received goal: {input_data['target_artefact']['payload']}")

# Run noisy tools - output goes to docker logs
subprocess.run(["pip", "install", "-r", "requirements.txt"], check=True)
print("Dependencies installed")

# Do work
result = do_work(input_data)
print(f"Work complete: {result}")

# Return result via FD 3
with os.fdopen(3, 'w') as fd3:
    json.dump({
        "artefact_type": "PythonResult",
        "artefact_payload": result,
        "summary": f"Processed {len(result)} items"
    }, fd3)
    fd3.write('\n')
```

### Node.js

```javascript
#!/usr/bin/env node
const fs = require('fs');

// Read input from stdin
let inputData = '';
process.stdin.on('data', chunk => inputData += chunk);
process.stdin.on('end', async () => {
  const input = JSON.parse(inputData);

  // Be noisy! Print to stdout/stderr
  console.log('Node agent starting...');
  console.log('Received:', input.target_artefact.payload);

  // Run noisy tools - output goes to docker logs
  const { execSync } = require('child_process');
  execSync('npm install', { stdio: 'inherit' });  // Full output visible
  console.log('Dependencies installed');

  // Do work
  const result = await doWork(input);
  console.log('Work complete');

  // Return result via FD 3
  const fd3 = fs.createWriteStream(null, { fd: 3 });
  fd3.write(JSON.stringify({
    artefact_type: 'NodeResult',
    artefact_payload: result,
    summary: `Processed ${result.length} items`
  }));
  fd3.end();
});
```

### Go

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
)

func main() {
	// Read input from stdin
	var input map[string]interface{}
	json.NewDecoder(os.Stdin).Decode(&input)

	// Be noisy! Print to stdout/stderr
	fmt.Println("Go agent starting...")
	fmt.Printf("Received: %v\n", input["target_artefact"])

	// Run noisy tools - output goes to docker logs
	cmd := exec.Command("go", "mod", "download")
	cmd.Stdout = os.Stdout  // Full output visible
	cmd.Stderr = os.Stderr
	cmd.Run()
	fmt.Println("Dependencies downloaded")

	// Do work
	result := doWork(input)
	fmt.Println("Work complete")

	// Return result via FD 3
	fd3 := os.NewFile(3, "fd3")
	defer fd3.Close()

	json.NewEncoder(fd3).Encode(map[string]interface{}{
		"artefact_type":    "GoResult",
		"artefact_payload": result,
		"summary":          fmt.Sprintf("Processed %d items", len(result)),
	})
}
```

---

## Helper Libraries

### Shell Helper (`agents/lib/log.sh`)

```bash
#!/bin/sh
# Source this file: . /app/lib/log.sh

# holt_return: Write JSON result to FD 3
holt_return() {
    json_result="$1"

    if [ -z "$json_result" ]; then
        echo "ERROR: holt_return requires JSON string argument" >&2
        return 1
    fi

    cat <<EOF >&3
$json_result
EOF
}

# Usage:
# . /app/lib/log.sh
# holt_return '{"artefact_type":"Test","artefact_payload":"data","summary":"ok"}'
```

### Python Helper (`agents/lib/log.py`)

```python
import os
import json
import sys

def holt_return(data):
    """Write result JSON to FD 3"""
    try:
        with os.fdopen(3, 'w') as fd3:
            json.dump(data, fd3)
            fd3.write('\n')
    except OSError:
        # FD 3 not available (running outside pup)
        # Fallback to stdout for local testing
        json.dump(data, sys.stdout)
        print()

# Usage:
# from log import holt_return
# holt_return({
#     "artefact_type": "Test",
#     "artefact_payload": "data",
#     "summary": "ok"
# })
```

---

## Common Mistakes and Fixes

### ❌ Mistake 1: Writing logs to FD 3

```bash
# WRONG - pollutes result JSON
echo "Starting work..." >&3
cat result.json >&3
```

```bash
# CORRECT - logs to stdout, result to FD 3
echo "Starting work..."
cat result.json >&3
```

### ❌ Mistake 2: Writing result to stdout

```bash
# WRONG - result goes to logs (pup won't see it)
echo '{"artefact_type":"Test","artefact_payload":"","summary":"ok"}'
```

```bash
# CORRECT - result goes to FD 3
cat <<EOF >&3
{"artefact_type":"Test","artefact_payload":"","summary":"ok"}
EOF
```

### ❌ Mistake 3: Silencing tools

```bash
# WRONG - can't see what happened
npm install > /dev/null 2>&1
```

```bash
# CORRECT - full output visible in holt logs
npm install
```

### ❌ Mistake 4: Not writing to FD 3

```bash
# WRONG - agent exits without result
echo "Work done!"
exit 0
```

```bash
# CORRECT - always write result to FD 3
echo "Work done!"
cat <<EOF >&3
{"artefact_type":"Success","artefact_payload":"","summary":"Done"}
EOF
```

---

## Viewing Agent Logs

Use the `holt logs` command to view agent output:

```bash
# View agent logs
holt logs coder-agent

# Follow logs in real-time
holt logs -f coder-agent

# Show last 100 lines
holt logs --tail=100 coder-agent

# Show logs from last hour
holt logs --since=1h orchestrator

# Combine flags
holt logs -f --since=30m --timestamps coder-agent

# Filter with grep
holt logs coder-agent | grep ERROR

# Filter with jq (if agent logs JSON)
holt logs coder-agent | grep '^{' | jq '.message'
```

---

## Debugging Tips

### Check if FD 3 is available

```bash
if [ -e /dev/fd/3 ]; then
    echo "FD 3 available"
else
    echo "FD 3 not available (running outside pup?)" >&2
    exit 1
fi
```

### Test agent locally (without FD 3)

```bash
# Redirect FD 3 to a file for local testing
./run.sh 3> result.json < input.json

# View result
cat result.json | jq .
```

### Common error messages

**"agent did not write result to FD 3"**
- Agent exited without writing to FD 3
- Fix: Always write JSON to `>&3` before exiting

**"FD 3 output does not start with JSON"**
- Agent wrote non-JSON to FD 3 (e.g., logs)
- Fix: Only write JSON result to FD 3, logs go to stdout/stderr

**"invalid JSON on FD 3"**
- JSON syntax error in result
- Fix: Validate JSON syntax (use `jq` or `json.tool`)

---

## Best Practices

### 1. Always Write Result to FD 3

Even if your agent fails, write a result:

```bash
if ! do_work; then
    cat <<EOF >&3
{
  "artefact_type": "Failure",
  "artefact_payload": "",
  "summary": "Work failed: $(cat error.log)"
}
EOF
    exit 1
fi
```

### 2. Use Structured Logging

Make logs easy to grep:

```bash
echo "[INFO] Starting phase 1..."
echo "[WARN] Retrying connection..."
echo "[ERROR] Failed to connect"
```

### 3. Log Progress for Long-Running Tasks

```bash
echo "[PROGRESS] 10% - Downloading dependencies..."
npm install
echo "[PROGRESS] 40% - Running tests..."
npm test
echo "[PROGRESS] 100% - Complete"
```

### 4. Don't Log Secrets

```bash
# WRONG - logs API key
echo "Using API key: $API_KEY"

# CORRECT - redact secrets
echo "Using API key: ${API_KEY:0:8}..."
```

### 5. Use Exit Codes Correctly

- `exit 0` - Success (must write result to FD 3)
- `exit 1` - Failure (should write Failure artefact to FD 3)

---

## Migration from Old Model

If you have agents using the old stdout-based model:

**Before (OLD)**:
```bash
result=$(do_work)
echo "{\"artefact_type\":\"Result\",\"artefact_payload\":\"$result\",\"summary\":\"Done\"}"
```

**After (NEW)**:
```bash
result=$(do_work)
cat <<EOF >&3
{
  "artefact_type": "Result",
  "artefact_payload": "$result",
  "summary": "Done"
}
EOF
```

**Key changes**:
1. Result goes to `>&3` instead of stdout
2. Remove all output silencing (`> /dev/null 2>&1`)
3. Add progress logs to stdout/stderr

---

## FAQ

**Q: Can I write multiline JSON to FD 3?**
A: Yes! FD 3 supports multiline JSON. Use heredocs or pretty-printed JSON.

**Q: What happens if I don't write to FD 3?**
A: Pup creates a Failure artefact with an error message.

**Q: Can I use FD 3 for streaming?**
A: No. FD 3 is read completely when the agent exits. Write your final result once.

**Q: Can I write multiple JSON objects to FD 3?**
A: No. Write one JSON object containing your entire result.

**Q: Does FD 3 have a size limit?**
A: Yes, 10MB. For larger results, write to a file and return a reference.

**Q: Can I test my agent without Docker?**
A: Yes, redirect FD 3: `./run.sh 3> result.json < input.json`

---

## See Also

- [Agent Interface Documentation](independent-holt-clan-creation/agent_interface.md)
- [Best Practices](independent-holt-clan-creation/best_practices.md)
- [CLI Reference - holt logs](independent-holt-clan-creation/cli_reference.md)
- [Design: Agent Pup](../design/agent-pup.md)
