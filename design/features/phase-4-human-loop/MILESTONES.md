# Phase 4: "Human-in-the-Loop" - Implementation Milestones

**Phase Goal**: To deliver a full-featured system with robust human oversight, production-grade data persistence, and advanced interactive capabilities, culminating in a powerful "dogfooding" demonstration.

**Phase Success Criteria**:
- Complex workflows can be paused for and resumed by human or agent-based clarification.
- Humans can interactively intercept and control workflows at predefined breakpoints.
- Holt instances can be stopped without data loss and permanently destroyed when required.
- The entire development lifecycle of a Holt feature can be demonstrated using Holt itself.

---

## **Milestone Overview**

Phase 4 focuses on adding sophisticated human-in-the-loop interaction, production-level data management, and proving the power of the platform by using it to develop itself.

### **M4.1: Advanced Question/Answer System**
**Status**: Design Complete
**Dependencies**: Phase 3 completion

**Goal**: Enable agents to ask clarifying questions to other agents or escalate to a human when necessary.

**Scope**:
- **Agent-to-Agent Questions**: Implement a mechanism for an agent working on a claim to create a `Question` artefact that is directed at the `produced_by_role` of a source artefact. The target agent can then produce a new version of the questioned artefact, triggering the M3.3 automated feedback loop.
- **Agent-to-Human Escalation**: If a question cannot be answered by another agent (or is directed to a `user` role), it should be escalated to the human operator.
- **CLI Tooling**: Implement `holt questions [--wait]` and `holt answer <question-id> "response"`.
- **Orchestrator Logic**: The orchestrator treats Questions as "late review feedback" that terminates the original claim and creates a `pending_assignment` claim for the original author to produce a clearer version.
- **Iteration Limits**: Questions reuse `orchestrator.max_review_iterations` to prevent infinite Q&A loops.

**Design Document**: [M4.1-advanced-question-answer-system.md](./M4.1-advanced-question-answer-system.md)

---

### **M4.2: Interactive Debugging & Control**
**Status**: Design Complete
**Dependencies**: Phase 3 completion

**Goal**: Provide a traditional, breakpoint-based interactive debugger that allows human operators to attach to a running Holt instance, pause workflows at specific conditions, inspect state, and manually intervene.

**Scope**:
- **`holt debug` Command**: A new top-level command that attaches to a running instance and starts an interactive debugging session with a `(holt-debug)` prompt.
- **Ephemeral Breakpoints**: Breakpoints are session-specific and exist only while the debugger is connected. When the debugger disconnects (gracefully or via crash), all breakpoints are automatically cleared and workflows resume.
- **Interactive Session Commands**: Support for traditional debugger commands:
  - `continue` (c) - Resume workflow until next breakpoint
  - `next` (n) - Single-step through orchestrator events
  - `break` (b) - Set breakpoint with glob patterns (e.g., `artefact.type=*Spec`, `claim.status=pending_review`)
  - `print` (p) - Inspect artefacts
  - `reviews` - List pending review claims
  - `review <claim-id> [--approve|--reject]` - Manually approve/reject reviews
  - `breakpoints` (bp) - List active breakpoints
  - `clear <id>` - Remove breakpoint
  - `exit` - Disconnect and clear all breakpoints
- **Pub/Sub Communication**: CLI and Orchestrator communicate via Redis Pub/Sub channels (`debug:command` and `debug:event`).
- **Orchestrator Pause Logic**: The orchestrator's main event loop checks for breakpoints before processing each event and blocks when paused, waiting for continue/step commands.
- **Context-Aware Manual Review**: The `review` command only works when paused on a `claim.status=pending_review` breakpoint, preventing mistakes.
- **Safety Features**:
  - Session heartbeat with 30-second TTL (auto-resume if debugger crashes)
  - Single active session only (prevent conflicting commands)
  - All manual interventions create proper Review artefacts for audit trail

**Design Document**: [M4.2-interactive-debugging-control.md](./M4.2-interactive-debugging-control.md)

---

### **M4.3: Context Caching**
**Status**: Design Complete (Approved)
**Dependencies**: None

**Goal**: Enable agents to perform an expensive, one-time context discovery and then cache that context on the Blackboard for efficient reuse across all subsequent runs and rework cycles for that thread of work.

**Scope**:
- **Checkpoint Side-Effect**: Enhance the agent tool's output contract to include an optional `checkpoints` array. This allows an agent to produce its main work artefact and checkpoint its context in a single turn.
- **Knowledge Artefact**: The pup will process the `checkpoints` array to create special `Knowledge` artefacts on the Blackboard, linked to the current work thread's `logical_id`.
- **Pup Logic**: The pup's context assembly logic will be enhanced to find these `Knowledge` artefacts and automatically inject their content into the agent's context on subsequent runs.
- **Agent Logic**: The pup will provide a `context_is_declared` flag to the agent, allowing the agent's tool script to know whether it needs to perform its one-time discovery logic or if the context has already been cached.
- **Global Knowledge Index**: A Redis hash maps globally unique knowledge names to version threads, preventing naming collisions.
- **CLI Provisioning**: New `holt provision` command for manually creating Knowledge artefacts from files, URLs, or commands.

**Design Document**: [M4.3-context-caching.md](./M4.3-context-caching.md)

---

### **M4.4: Production-Grade State Management**
**Status**: Not Started
**Dependencies**: None

**Goal**: Ensure that Holt instances are persistent by default and can be explicitly destroyed when no longer needed.

**Scope**:
- **Persistent Redis Storage**: Modify the `holt up` command to create and use a persistent data volume for the Redis container, likely in a local `~/.holt/instances/<instance_name>/` directory. This path should be configurable.
- **Clarify `holt down`**: The `holt down` command will stop the service containers but will **not** delete the persistent Redis data volume.
- **Implement `holt destroy`**: Create the `holt destroy --name <instance>` command. This command will stop the instance (if running) and permanently delete the associated Redis data volume from the host machine.
- **Safety Features**: The `destroy` command must include safety checks, such as requiring the instance name to be typed out for confirmation, as specified in the original design.

**Design Document**: TBD

---

### **M4.5: The Holt Development Lifecycle Demo**
**Status**: Not Started
**Dependencies**: M4.1, M4.2, M4.3, M4.4

**Goal**: Create the ultimate "dogfooding" demo, showcasing a team of Holt agents building a new feature for Holt itself.

**Scope**:
- **Demo Scenario**: The goal will be to "add a new `holt stats` command that shows the number of artefacts and claims".
- **New Agents**: Create a clan of agents for the demo:
    - `designer-agent`: Creates a feature design document.
    - `go-coder-agent`: Implements the Go code for the CLI command and tests. It will use the new Context Caching feature to load relevant design docs.
    - `reviewer-agent`: Reviews code and design documents.
    - `test-runner-agent`: A tool-based agent that runs `make test` and reports results.
- **Workflow**: The demo will showcase the full lifecycle: design -> review -> implementation -> testing -> human approval, including the potential for agent-to-agent questions and human breakpoints.

**Design Document**: TBD

---

### **M4.6: Production Documentation**
**Status**: Not Started
**Dependencies**: All other M4 milestones.

**Goal**: Document all new Phase 4 features for end-users.

**Scope**:
- Create user guides for the Q&A system, interactive debugging, and context caching features.
- Document the new persistent state model and the `holt down` vs. `holt destroy` behavior.
- Create a new "How-To" guide walking through the new `holt-development-lifecycle-demo`.
- Update all relevant sections of the `README.md`, `QUICK_REFERENCE.md`, etc.