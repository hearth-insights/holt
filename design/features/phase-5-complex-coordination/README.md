# **Phase 5: Complex Workflow Coordination**

**Goal**: Enable the orchestration of complex, non-linear workflows involving multi-dependency synchronization ("fan-in") and conditional pathing, moving Holt from a phased execution model to a full Directed Acyclic Graph (DAG) coordination platform.

**Status**: **In Progress** - M5.1 Complete ✅

## **Phase Success Criteria**

- ✅ **M5.1**: A "synchronizer" or "aggregator" agent can be built that waits for several distinct artefacts from different workflow branches to be completed before it begins its own work.
- ⏳ **M5.2+**: The system can support conditional execution paths, where the creation of one type of artefact (e.g., `HighSeverityBugFound`) can trigger a completely different set of agents than another artefact (e.g., `TestsPassed`).
- ⏳ **M5.2+**: Complex, real-world CI/CD pipelines, including build, multi-platform testing, security scanning, and conditional deployment, can be fully modeled and executed within Holt.
- ✅ **M5.1**: The `holt watch` output and `holt hoard` audit trail can be used to clearly visualize and debug these complex, branching, and merging workflows.

## **Milestones**

### **M5.1: Declarative Fan-In Synchronization** ✅ **Complete**

Enables agents to declaratively wait for multiple prerequisite artefacts before executing.

**Status**: Implementation complete, tested, documented

**Key Features:**
- **Named Pattern**: Wait for specific artefact types (e.g., TestResult + LintResult + SecurityScan)
- **Producer-Declared Pattern**: Wait for N artefacts (N from runtime metadata)
- **Multi-Artefact Output**: Agents can create multiple artefacts in one execution
- **Atomic Indexing**: Reverse index for efficient graph traversal
- **Deduplication Locks**: Prevent race conditions in concurrent workflows

**Deliverables:**
- ✅ Blackboard Lua script for atomic artefact creation
- ✅ Reverse index (parent → children) for descendant traversal
- ✅ Pup synchronizer mode with declarative configuration
- ✅ Multi-artefact buffer-and-flush pattern
- ✅ Comprehensive documentation and examples
- ✅ 53 unit/integration/E2E tests (1,969 lines)

**Example Agents:**
- `agents/example-deployer-agent/` - Named pattern (CI/CD deployment)
- `agents/example-batch-aggregator-agent/` - Producer-Declared pattern (data processing)

**Documentation:**
- Design: `design/features/phase-5-complex-coordination/M5.1-fan-in.md`
- Guide: `docs/guides/fan-in-synchronization.md`
- Reference: `docs/guides/agent-development.md` (Synchronizer Agents section)

---

### **M5.2: Automatic Workflow Completion** ⏳ **Next**

Enable the Orchestrator to automatically detect workflow completion when no explicit Terminal artefact is produced.

**Status**: Design complete, implementation pending

**Key Features:**
- **Quiescence Detection**: Automatically detect when all work is finished (all claims complete, no unclaimed artefacts)
- **Auto-Termination**: Generate Terminal artefact when workflow reaches dead end
- **Configurable**: `orchestrator.auto_terminate` setting to enable/disable
- **Grace Period**: Configurable delay before termination to prevent jitter
- **Watch Integration**: `holt watch --exit-on-completion` automatically exits

**Design Document**: `design/features/phase-5-complex-coordination/M5.2-automatic-workflow-completion.md`

---

### **M5.3+: Future Enhancements** 💡 **Planned**

Addressing M5.1 known limitations and expanding DAG capabilities:

1.  **Timeout-Based Synchronization** (Addresses M5.1 Limitation: Partial Fan-In Hang)
    *   Add `timeout` configuration to synchronizers (e.g., "wait 10 minutes for all prerequisites")
    *   Create Failure artefact if timeout expires with diagnostic information
    *   Prevent indefinite hangs on partial failures (4 of 5 shards complete scenario)
    *   **Impact**: Resolves production blocker for long-running batch workflows

2.  **Orphaned Lock Cleanup** (Addresses M5.1 Limitation: Deadlock on Pup Crash)
    *   Heartbeat-based lock monitoring
    *   Automatic lock cleanup on Pup crash detection
    *   Configurable TTL with early release on health check failure
    *   **Impact**: Reduces 10-minute deadlock window to seconds

3.  **Reverse Index Garbage Collection** (Addresses M5.1 Limitation: Unbounded Growth)
    *   `holt gc` command to clean up completed workflow indices
    *   Automatic cleanup based on artefact age or instance lifecycle
    *   Configurable retention policies
    *   **Impact**: Enables long-running instances without memory bloat

4.  **Cross-Ancestor Synchronization**
    *   Wait for descendants of multiple ancestors (not just one)
    *   Example: Merge results from parallel CodeCommit branches
    *   **Impact**: Enables complex merge scenarios in multi-branch workflows

5.  **Conditional Pathing**
    *   Leverage dynamic bidding for highly conditional workflows
    *   Example: Bid on `TestResult` only if payload contains `"status": "failed"`
    *   Creates dedicated "failure recovery" branches
    *   **Impact**: Enables conditional DAG execution paths

6.  **Dynamic Wait Conditions**
    *   Runtime-configurable synchronization requirements
    *   Modify `wait_for` based on workflow state
    *   **Impact**: Enables adaptive workflow patterns

## **Implementation Constraints**

- The primary mechanism for this phase will be the implementation of more intelligent agent bidding logic, rather than significant changes to the orchestrator.
- The orchestrator must remain a stateless, non-intelligent arbiter. All workflow branching and synchronization logic must reside within the agents' bidding strategies.
- Performance of blackboard queries during bidding will become a key consideration.

## **Dependencies**

- This phase builds upon all previous phases, requiring a stable multi-agent coordination model (Phase 3) and robust human-in-the-loop capabilities (Phase 4).

---

## **M5.1 Implementation Detail: Declarative Fan-In Synchronization**

*This section contains the detailed design for the Fan-In Synchronization feature, to be broken into formal milestones upon implementation of Phase 5.*

### **Problem Statement**

Holt's phased execution model excels at linear workflows and parallel "fan-out" operations (e.g., multiple reviewers acting on one artefact). However, it lacks a formal, robust mechanism for the reverse: **"fan-in" synchronization**. There is no easy way to define an agent that should only run *after* several different, parallel workflow branches have all completed.

A naive implementation would require the synchronizing agent to contain complex, brittle, and race-condition-prone logic in its bidding script to query the blackboard and correlate disparate results. This is unsafe, as it could erroneously merge results from different workflow forks, and it violates the principle of keeping agent logic simple.

### **The Solution: Declarative Synchronization**

To solve this, "fan-in" will be a first-class feature of Holt, implemented in the Pup and declared in `holt.yml`.

#### **New `holt.yml` Configuration**

A new, optional `synchronize` block will be added to the agent definition. An agent with this block is designated as a "Synchronizer Agent."

```yaml
agents:
  Deployer:
    image: "holt-deployer-demo:latest"
    command: ["/app/run.sh"]
    # The "synchronize" block replaces bid_script and bidding_strategy
    synchronize:
      on:
        # The agent will look for this common ancestor type.
        ancestor_type: "CodeCommit"
        # And will wait until ALL of these descendant types exist
        # as direct children of that single ancestor.
        require_descendants:
          - "TestResult"
          - "LintResult"
          - "Documentation"
      # The bid to place on the final trigger artefact once all conditions are met.
      bid: "exclusive"
```

#### **The Pup's New Synchronization Logic**

When an agent pup starts, it will detect the `synchronize` block in its configuration and enter a new bidding mode. For every incoming claim, the `claimWatcher` will execute this logic:

1.  **Identify Potential Trigger:** Check if the claim's target artefact has a `type` that is listed in the `require_descendants` array. If not, `ignore` the claim immediately.

2.  **Find Common Ancestor:** If the artefact is a potential trigger, traverse **up** the artefact graph via its `source_artefacts`. Find the first ancestor whose `type` matches the `ancestor_type` from the configuration (e.g., `CodeCommit`). If no such ancestor is found, `ignore` the claim.

3.  **Verify All Dependencies (The Fan-In Check):** Once the common ancestor is found, the pup will perform a **full descendant traversal** starting from that ancestor to find all artefacts that have it in their provenance chain. It will then check if this complete set of descendants contains an artefact for **every single type** listed in the `require_descendants` array.

4.  **Make Bid Decision:**
    *   If all required descendant artefacts are present, the condition is met. The pup will submit the configured `bid` (e.g., `exclusive`) on the current claim (which was for the final artefact that completed the set).
    *   If any descendant artefact is still missing, the pup will submit `ignore`.

This logic ensures that the bid is only placed when the complete set of required inputs, all stemming from a single, common ancestor, is available.

#### **Agent Execution and Context**

When the Synchronizer agent (e.g., `Deployer`) is finally granted the claim, the pup will assemble a special, rich `context_chain` to pass to its `run.sh` script. This context will include:

*   The common `ancestor_artefact` (e.g., the `CodeCommit`).
*   The full list of all the `descendant_artefacts` that satisfied the condition (e.g., the `TestResult`, `LintResult`, and `Documentation` artefacts).

This provides the agent with all the necessary inputs to perform its aggregation or deployment task.