#!/bin/sh
set -e

# 1. Read Raw Input
raw_input=$(cat)

# 2. Trim Context (using python script)
# Ensure python is installed in your container
trimmed_input=$(echo "$raw_input" | python3 /app/examples/trim_context.py)

# 3. Pass Trimmed Input to Agent Logic
# You can pass it via stdin or environment variable
echo "$trimmed_input" | python3 /app/agent_logic.py
