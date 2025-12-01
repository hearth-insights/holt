import sys
import json
import os

# Configuration
MAX_CONTEXT_ITEMS = 5  # Keep only the 5 most recent items
MAX_TOTAL_CHARS = 16000 # Rough character limit (approx 4k tokens)

def main():
    # Read full JSON input from stdin
    try:
        input_data = json.load(sys.stdin)
    except json.JSONDecodeError:
        print("Error: Invalid JSON input", file=sys.stderr)
        sys.exit(1)

    target_artefact = input_data.get("target_artefact", {})
    context_chain = input_data.get("context_chain", [])

    # Sort context by creation time (newest first) if available, or assume list order is chronological
    # Holt context_chain is typically ordered [oldest -> newest]
    # We want to keep the NEWEST items.
    
    # 1. Prioritize Target Artefact (Always keep)
    final_context = []
    current_chars = len(json.dumps(target_artefact))

    # 2. Filter Context Chain
    # Reverse to process newest first
    reversed_chain = list(reversed(context_chain))
    
    kept_items = []
    for item in reversed_chain:
        item_str = json.dumps(item)
        item_len = len(item_str)
        
        if len(kept_items) >= MAX_CONTEXT_ITEMS:
            break
            
        if current_chars + item_len > MAX_TOTAL_CHARS:
            break
            
        kept_items.append(item)
        current_chars += item_len

    # Restore chronological order
    final_context = list(reversed(kept_items))

    # Construct new input object
    trimmed_input = {
        "target_artefact": target_artefact,
        "context_chain": final_context
    }

    # Output trimmed JSON to stdout
    print(json.dumps(trimmed_input))

if __name__ == "__main__":
    main()
