# 8. Agent Interface (Pup Contract)

Holt agents are Docker containers that run a specific binary called `pup`. The `pup` binary handles communication with the Blackboard (Redis) and invokes your custom logic via two entry points: **Bid** and **Run**.

## The Pup Contract
`pup` acts as an adapter and a **circuit breaker**. It:
1.  Watches the Blackboard for new artefacts.
2.  Invokes your **Bid Script** to see if the agent wants to work.
3.  If the bid is accepted, invokes your **Run Command** to perform the work.
4.  **Enforces Schema**: Validates your agent's JSON output. If the output is malformed or violates the schema, `pup` rejects it and creates a `Failure` artefact. This prevents "hallucinations" from corrupting the Blackboard.
5.  Publishes the result back to the Blackboard.

Communication happens via **Standard Input (stdin)** and **Standard Output (stdout)**.

---

## 1. Bid Entry Point
**Purpose**: Decide if the agent should act on a specific artefact.

*   **Trigger**: A new artefact is published to the Blackboard.
*   **Command**: Defined in `holt.yaml` under `bid_script` (default: `/app/bid.sh`).

### Input (stdin)
A JSON object representing the artefact that just appeared.

```json
{
  "id": "artefact-123",
  "type": "GoalDefined",
  "payload": "Create a recipe for toast",
  "created_at": "2023-10-27T10:00:00Z"
}
```

### Output (stdout)
A single string indicating the bidding strategy.

| Strategy | Description |
| :--- | :--- |
| `exclusive` | I want to do this. Only one agent of my type can run at a time. |
| `review` | I want to review this. I run first, in parallel with other reviewers. |
| `claim` | I want to do this. I run after reviews, in parallel with others. |
| `ignore` | I do not want to work on this. |

### Static Bidding Strategy
If no `bid_script` is defined in `holt.yaml`, the agent uses the static `bidding_strategy` defined in the configuration. This **must** be an object specifying the `type` and optionally `target_types`.

```yaml
bidding_strategy:
  type: "exclusive"
  target_types: ["GoalDefined", "AnotherType"]
```

*   **`type`**: The bid type (`exclusive`, `review`, `claim`, or `ignore`).
*   **`target_types`**: A list of artefact types to bid on. If omitted or empty, the agent bids on **all** artefact types.

> [!WARNING]
> **Breaking Change**: The legacy string format (e.g., `bidding_strategy: "exclusive"`) is no longer supported and will cause a validation error.

### Example `bid.sh`
```bash
#!/bin/sh
set -e

# Read input JSON
input=$(cat)

# Parse artefact type (requires jq)
type=$(echo "$input" | jq -r '.type')

# Logic: Only bid on "GoalDefined" artefacts
if [ "$type" = "GoalDefined" ]; then
    echo "exclusive"
else
    echo "ignore"
fi
```

---

## 2. Run Entry Point
**Purpose**: Perform the actual work and produce a result.

*   **Trigger**: The Orchestrator accepted your bid.
*   **Command**: Defined in `holt.yaml` under `command` (default: `/app/run.sh`).

### Input (stdin)
A JSON object containing the full context.

```json
{
  "target_artefact": {
    "id": "artefact-123",
    "type": "GoalDefined",
    "payload": "Create a recipe for toast"
  },
  "context_chain": [
    { "id": "artefact-100", "type": "GoalDefined", ... },
    { "id": "artefact-101", "type": "Plan", ... }
  ]
}
```

#### The Context Chain
The `context_chain` is a chronological list of all artefacts that led to the current moment.
*   **Standard Flow**: `Goal` -> `Plan` -> `Code`
*   **Review Loop (Rework)**: If your agent is fixing a mistake, the chain will look like this:
    1.  `Goal` (Original request)
    2.  `Code` (Your previous attempt)
    3.  `Review` (The feedback explaining why it failed)

**Tip**: To fix a mistake, your agent should look at the **last item** in the `context_chain` (the Review) to understand what went wrong, and the **second-to-last item** (the Code) to see what it wrote previously.

#### Managing Large Contexts
The `context_chain` can grow large. If you are using a local LLM with a small context window, you should trim this list.
*   **See Example**: [examples/trim_context.py](./examples/trim_context.py) demonstrates how to prioritize recent items and trim the context.

### Output (stdout)
A JSON object representing the **Result Artefact** you produced.

```json
{
  "artefact_type": "RecipeYAML",
  "artefact_payload": "title: Toast\n...",
  "summary": "Created recipe for toast"
}
```

> [!IMPORTANT]
> **Review Claim Enforcement**: If your agent bid `review` and was granted the claim, it **MUST** produce an artefact with `artefact_type` set to `Review` (or `StructuralType: Review` in the underlying schema).
>
> **Strict Approval Rule**: To **APPROVE**, the payload MUST be an empty JSON object `{}` or array `[]`. **ANY** other content is treated as a rejection and triggers rework.

### Example `run.sh`
```bash
#!/bin/sh
set -e

# Read input
input=$(cat)

# Extract payload
goal=$(echo "$input" | jq -r '.target_artefact.payload')

# Do work (e.g., generate content)
result="Here is the recipe for: $goal"

# Output result JSON
jq -n \
  --arg type "RecipeResult" \
  --arg payload "$result" \
  --arg summary "Generated recipe" \
  '{artefact_type: $type, artefact_payload: $payload, summary: $summary}'
```

## Summary
1.  **Bid**: `Input JSON` -> `Strategy String`
2.  **Run**: `Input JSON` -> `Result JSON`
