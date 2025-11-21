# Holt Development Lifecycle Demo (M4.5)

**Purpose:** Demonstrate Holt's capability to manage complex, multi-stage development processes from high-level goal to tested, reviewed implementation.

## Overview

This demo showcases **two complete workflows** with **8 specialized agents**:

### Workflow 1: Design Phase
1. **GoalDefined** → DesignerGeminiAgent creates **DesignSpecDraft**
2. **HumanReviewerAgent** reviews and approves/rejects draft
3. **ClaudeDesignAgent** expands draft into complete **DesignSpec**
4. **HumanReviewerAgent** performs final approval
5. Creates new **GoalDefined** for implementation

### Workflow 2: Implementation Phase
1. **GoalDefined** → CoderClaudeAgent implements code → **ChangeSet**
2. **TestRunnerAgent** + **SecurityScannerAgent** review in parallel
3. **LeadEngineerGeminiAgent** performs final review
4. Creates **Terminal** artefact (completion)

## Quick Start

### Prerequisites

1. **Holt installed:**
   ```bash
   cd /path/to/holt
   make build
   ```

2. **Docker running** (required for agents)

3. **Git repository** (any clean repo will work)

### Build Agent Images

```bash
# From holt root directory
docker build -t holt/gemini-agent:latest -f agents/gemini-agent/Dockerfile .
docker build -t holt/claude-agent:latest -f agents/claude-agent/Dockerfile .
docker build -t holt/human-reviewer:latest -f agents/human-reviewer/Dockerfile .
docker build -t holt/test-runner:latest -f agents/test-runner/Dockerfile .
docker build -t holt/security-scanner:latest -f agents/security-scanner/Dockerfile .
```

### Run the Demo

```bash
# 1. Navigate to a test project (or create one)
mkdir /tmp/holt-demo && cd /tmp/holt-demo
git init
git commit --allow-empty -m "Initial commit"

# 2. Copy demo configuration
cp /path/to/holt/demos/dev-lifecycle-demo/holt.yml .

# 3. Start Holt instance
holt up

# 4. Trigger design workflow
holt forage --goal "Design a simple feature tracking system"

# 5. Monitor progress
holt watch

# 6. View audit trail
holt hoard
```

## Configuration Modes

### Mock Mode (Default - No API Keys Required)

All LLM agents run in `MOCK_MODE=true` by default, returning hardcoded responses:

```yaml
agents:
  Designer:
    environment:
      - MOCK_MODE=true  # Returns mock responses
```

**Advantages:**
- No API keys needed
- Fast, deterministic responses
- Perfect for testing and development

### Real API Mode (Requires API Keys)

To use real Gemini and Claude APIs:

```yaml
agents:
  Designer:
    environment:
      - MOCK_MODE=false
      - GEMINI_API_KEY=${GEMINI_API_KEY}

  Coder:
    environment:
      - MOCK_MODE=false
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
```

Then export your API keys:

```bash
export GEMINI_API_KEY="your-gemini-key"
export ANTHROPIC_API_KEY="your-claude-key"
holt up
```

### Human Review Modes

#### Auto-Approve (Testing)
```yaml
HumanReviewer:
  environment:
    - AUTO_APPROVE=true  # Automatically approves all reviews
```

#### Interactive (Real Review)
```yaml
HumanReviewer:
  environment:
    - AUTO_APPROVE=false  # Prompts for human input
    - REVIEW_TIMEOUT=300  # 5 minutes to respond
```

## Authentication Methods

### Method 1: API Keys (Recommended)

**Best for:** Portable, CI/CD, production

```yaml
agents:
  Designer:
    environment:
      - GEMINI_API_KEY=${GEMINI_API_KEY}
```

### Method 2: Volume Mounts (Advanced)

**Best for:** Local development with gcloud CLI

⚠️ **SECURITY WARNING:** Exposes host filesystem to containers!

```yaml
agents:
  Designer:
    volumes:
      - "~/.config/gcloud:/root/.config/gcloud:ro"  # READ-ONLY!
```

**Security Checklist:**
- [ ] Verify agent image is from trusted source
- [ ] Always use `:ro` (read-only) mode
- [ ] Mount only specific directories (not entire home)
- [ ] Consider using API keys instead if possible

## Agent Roles

| Agent | Role | Input | Output | Mode |
|-------|------|-------|--------|------|
| Designer | Design proposal | GoalDefined | DesignSpecDraft | Exclusive |
| HumanReviewer | Approve/reject | Any artefact | Review | Review |
| DetailedDesigner | Expand design | DesignSpecDraft | DesignSpec | Exclusive |
| Coder | Implement code | DesignSpec | ChangeSet | Exclusive |
| Architect | Answer questions | Question | Answer | Exclusive |
| TestRunner | Run tests | ChangeSet | Review | Review |
| SecurityScanner | Security scan | ChangeSet | Review | Review |
| LeadEngineer | Final approval | ChangeSet | Terminal | Exclusive |

## Workflow Diagrams

### Design Workflow
```
GoalDefined
    ↓
DesignerGemini (DesignSpecDraft)
    ↓
HumanReviewer (Review: approve/reject)
    ↓ (if approved)
ClaudeDesign (DesignSpec)
    ↓
HumanReviewer (Review: approve/reject)
    ↓ (if approved)
GoalDefined (for implementation)
```

### Implementation Workflow
```
GoalDefined
    ↓
CoderClaude (ChangeSet)
    ↓
    ├─→ TestRunner (Review: pass/fail)
    └─→ SecurityScanner (Review: pass/fail)
    ↓ (both must pass)
LeadEngineer (Terminal: approve)
```

## Rework Loops

If reviews fail, M3.3 automated feedback loop triggers:

```
ChangeSet v1
    ↓
TestRunner → FAIL (feedback: "Tests missing")
    ↓
CoderClaude (receives feedback)
    ↓
ChangeSet v2
    ↓
TestRunner → PASS
    ↓
(continues to SecurityScanner)
```

**Max iterations:** 3 (configurable in `orchestrator.max_review_iterations`)

## Troubleshooting

### Agent Not Starting

```bash
# Check agent logs
holt logs Designer

# Verify image exists
docker images | grep holt

# Rebuild image
docker build -t holt/gemini-agent:latest -f agents/gemini-agent/Dockerfile .
```

### Review Timeout

```bash
# Increase timeout
# In holt.yml:
HumanReviewer:
  environment:
    - REVIEW_TIMEOUT=600  # 10 minutes
```

### Tests Fail

```bash
# Check what tests are failing
holt hoard | grep TestRunner

# View test output in Review feedback
# The Review artefact will contain test failure details
```

## Example Session

```bash
# Start demo
$ cd /tmp/demo && git init && git commit --allow-empty -m "init"
$ cp ~/holt/demos/dev-lifecycle-demo/holt.yml .
$ holt up

# Trigger workflow
$ holt forage --goal "Design a user authentication system"

# Watch progress
$ holt watch
[2025-11-14 20:10:00] ARTEFACT_CREATED: GoalDefined (abc-123)
[2025-11-14 20:10:01] CLAIM_CREATED: Claim for abc-123
[2025-11-14 20:10:02] GRANT: Designer granted exclusive claim
[2025-11-14 20:10:05] ARTEFACT_CREATED: DesignSpecDraft (def-456)
[2025-11-14 20:10:06] CLAIM_CREATED: Claim for def-456
[2025-11-14 20:10:07] GRANT: HumanReviewer granted review claim
[2025-11-14 20:10:08] ARTEFACT_CREATED: Review (approved) (ghi-789)
[2025-11-14 20:10:09] GRANT: DetailedDesigner granted exclusive claim
...
[2025-11-14 20:15:30] ARTEFACT_CREATED: Terminal (workflow complete)

# View audit trail
$ holt hoard
Showing all artefacts:
- GoalDefined: "Design a user authentication system"
- DesignSpecDraft: High-level design proposal
- Review: Approved by human
- DesignSpec: Complete 10-section specification
- Review: Approved by human
- GoalDefined: "Implement design spec xyz-789"
- ChangeSet: Implementation with tests (commit: abc123)
- Review: Tests passed
- Review: No security issues
- Terminal: Implementation approved
```

## Next Steps

1. **Customize agents:** Modify prompts in `holt.yml`
2. **Add real APIs:** Switch to `MOCK_MODE=false` with API keys
3. **Extend workflow:** Add more agents (e.g., DocumentationAgent)
4. **Integrate M4.3:** Provision Knowledge artefacts for Architect

## Documentation

- **M4.5 Design Doc:** `design/features/phase-4-human-loop/M4.5-holt-development-lifecycle-demo.md`
- **Agent READMEs:** `agents/*/README.md`
- **Holt Docs:** Root `README.md` and `docs/`

## Support

Issues: https://github.com/dyluth/holt/issues
