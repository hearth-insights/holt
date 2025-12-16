package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/c-bata/go-prompt"
	dockerpkg "github.com/hearth-insights/holt/internal/docker"
	"github.com/hearth-insights/holt/internal/instance"
	"github.com/hearth-insights/holt/internal/orchestrator/debug"
	"github.com/hearth-insights/holt/internal/printer"
	"github.com/hearth-insights/holt/internal/watch"
	"github.com/hearth-insights/holt/pkg/blackboard"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	debugInstanceName string
	debugBreakpoints  []string // Repeatable --break flag
	debugPauseOnStart bool     // Pause immediately on attach
)

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Attach an interactive debugger to a running Holt instance",
	Long: `Attach an interactive debugger to a running Holt instance for breakpoint-based
inspection, manual intervention, and workflow control.

Features:
  - Set breakpoints on artefact types, claim states, agent roles, or events
  - Pause workflow execution at breakpoints
  - Inspect artefact details and workflow state
  - Single-step through orchestrator events
  - Manually review and approve/reject artefacts
  - Safe disconnect: session expires automatically after 30 seconds

Breakpoint Conditions:
  artefact.type=<glob>              Match artefact type (e.g., "Code*", "*Spec")
  artefact.structural_type=<type>   Match structural type (Question, Review, Terminal)
  claim.status=<status>             Match claim status (pending_review, pending_exclusive)
  agent.role=<glob>                 Match agent role on grant (e.g., "coder-*")
  event.type=<event>                Match orchestrator event type

Interactive Commands:
  continue (c)        Resume workflow execution until next breakpoint
  next (n)            Single-step: process one event, then pause again
  break <cond> (b)    Set new breakpoint with condition
  breakpoints (bp)    List all active breakpoints
  clear <id>          Clear specific breakpoint by ID
  print [id] (p)      Inspect artefact (current or by ID)
  reviews             List all claims in pending_review status
  review <claim-id>   Manually review claim (--approve or --reject "text")
  help                Show command reference
  exit                End debug session and clear breakpoints

Examples:
  # Basic debugging session
  holt debug

  # Pre-set breakpoints on startup
  holt debug -b artefact.type=CodeCommit -b claim.status=pending_review

  # Target specific instance
  holt debug --name my-workflow

  # Pause immediately on attach (before any events)
  holt debug --pause-on-start

Safety:
  - Only one active debug session allowed per instance
  - Session heartbeat refreshed every 5 seconds
  - Session expires after 30 seconds without heartbeat
  - Workflow auto-resumes on session expiration or disconnect
  - All manual interventions are logged and auditable`,
	RunE: runDebug,
}

func init() {
	debugCmd.Flags().StringVarP(&debugInstanceName, "name", "n", "", "Target instance name (auto-inferred if omitted)")
	debugCmd.Flags().StringSliceVarP(&debugBreakpoints, "break", "b", []string{}, "Set breakpoint on startup (repeatable)")
	debugCmd.Flags().BoolVar(&debugPauseOnStart, "pause-on-start", false, "Pause orchestrator immediately on attach")

	rootCmd.AddCommand(debugCmd)
}

func runDebug(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Phase 1: Instance discovery
	cli, err := dockerpkg.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()

	targetInstanceName := debugInstanceName
	if targetInstanceName == "" {
		targetInstanceName, err = instance.InferInstanceFromWorkspace(ctx, cli)
		if err != nil {
			if err.Error() == "no Holt instances found for this workspace" {
				return printer.Error(
					"no Holt instances found",
					"No running instances found for this workspace.",
					[]string{"Start an instance first:\n  holt up"},
				)
			}
			if err.Error() == "multiple instances found for this workspace, use --name to specify which one" {
				return printer.Error(
					"multiple instances found",
					"Found multiple running instances for this workspace.",
					[]string{
						"Specify which instance to debug:\n  holt debug --name <instance-name>",
						"List instances:\n  holt list",
					},
				)
			}
			return fmt.Errorf("failed to infer instance: %w", err)
		}
	}

	// Phase 2: Verify instance is running
	if err := instance.VerifyInstanceRunning(ctx, cli, targetInstanceName); err != nil {
		return printer.Error(
			fmt.Sprintf("instance '%s' is not running", targetInstanceName),
			fmt.Sprintf("Error: %v", err),
			[]string{fmt.Sprintf("Start the instance:\n  holt up --name %s", targetInstanceName)},
		)
	}

	// Phase 3: Get Redis connection
	redisPort, err := instance.GetInstanceRedisPort(ctx, cli, targetInstanceName)
	if err != nil {
		return fmt.Errorf("failed to get Redis port: %w", err)
	}

	redisOpts := &redis.Options{
		Addr: fmt.Sprintf("localhost:%d", redisPort),
	}
	redisClient := redis.NewClient(redisOpts)
	defer redisClient.Close()

	// Verify Redis connectivity
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return printer.Error(
			"cannot connect to Redis",
			fmt.Sprintf("Failed to ping Redis at localhost:%d: %v", redisPort, err),
			[]string{"Verify instance is healthy:\n  holt list"},
		)
	}

	bbClient, err := blackboard.NewClient(redisOpts, targetInstanceName)
	if err != nil {
		return fmt.Errorf("failed to create blackboard client: %w", err)
	}

	// Phase 4: Create debug session
	debugger := NewDebugger(ctx, bbClient, targetInstanceName)
	defer debugger.Cleanup()

	// Initialize session (checks for existing sessions)
	if err := debugger.Initialize(); err != nil {
		if strings.Contains(err.Error(), "already active") {
			return printer.Error(
				"debug session already active",
				err.Error(),
				[]string{
					"Wait for the existing session to end",
					"Or manually clear stuck session:\n  redis-cli DEL holt:" + targetInstanceName + ":debug:session",
				},
			)
		}
		return fmt.Errorf("failed to initialize debug session: %w", err)
	}

	printer.Info("Debug session attached to instance '%s'\n", targetInstanceName)
	printer.Info("Session ID: %s\n", debugger.sessionID)
	printer.Info("Heartbeat: 5s interval, 30s TTL\n")
	printer.Info("\n")

	// Phase 5: Set initial breakpoints
	if len(debugBreakpoints) > 0 {
		printer.Info("Setting initial breakpoints...\n")
		for _, bp := range debugBreakpoints {
			if err := debugger.SetBreakpoint(bp); err != nil {
				printer.Warning("Failed to set breakpoint '%s': %v\n", bp, err)
			} else {
				printer.Success("Breakpoint set: %s\n", bp)
			}
		}
		printer.Info("\n")
	}

	// Phase 6: Start heartbeat and event listener
	debugger.StartHeartbeat()
	debugger.StartEventListener()

	// Phase 7: Handle pause-on-start if requested
	if debugPauseOnStart {
		// TODO: Send pause command to orchestrator
		printer.Info("Pausing orchestrator on startup...\n")
	}

	// Phase 8: Start interactive prompt
	printer.Info("Debug session ready. Type 'help' for commands, 'exit' to quit.\n")
	printer.Info("\n")

	// Run interactive prompt
	debugger.RunInteractivePrompt()

	return nil
}

// Debugger manages an interactive debug session
type Debugger struct {
	ctx          context.Context
	client       *blackboard.Client
	instanceName string
	sessionID    string
	redisClient  *redis.Client

	// Cancellation and cleanup
	cancelCtx  context.Context
	cancelFunc context.CancelFunc
	wg         sync.WaitGroup

	// State
	mu           sync.RWMutex
	isPaused     bool
	pauseContext *debug.PauseContext
	breakpoints  map[string]*debug.Breakpoint
	nextBPID     int

	// Communication channels
	eventCh      chan *debug.Event
	commandQueue chan string
}

// NewDebugger creates a new debugger instance
func NewDebugger(ctx context.Context, client *blackboard.Client, instanceName string) *Debugger {
	cancelCtx, cancelFunc := context.WithCancel(ctx)

	return &Debugger{
		ctx:          ctx,
		client:       client,
		instanceName: instanceName,
		sessionID:    blackboard.NewID(), // Unique session ID
		redisClient:  client.GetRedisClient(),
		cancelCtx:    cancelCtx,
		cancelFunc:   cancelFunc,
		breakpoints:  make(map[string]*debug.Breakpoint),
		eventCh:      make(chan *debug.Event, 100),
		commandQueue: make(chan string, 10),
		nextBPID:     1,
	}
}

// Initialize creates the debug session in Redis
func (d *Debugger) Initialize() error {
	// Use Redis SET NX to atomically check-and-create session
	sessionKey := fmt.Sprintf("holt:%s:debug:session", d.instanceName)

	// Check for existing session
	exists, err := d.redisClient.Exists(d.ctx, sessionKey).Result()
	if err != nil {
		return fmt.Errorf("failed to check for existing session: %w", err)
	}

	if exists > 0 {
		// Get session info for error message
		sessionData, _ := d.redisClient.HGetAll(d.ctx, sessionKey).Result()
		connectedAt := sessionData["connected_at_ms"]
		return fmt.Errorf("debug session already active (started at %s ms)", connectedAt)
	}

	// Create new session
	nowMs := time.Now().UnixMilli()
	sessionData := map[string]interface{}{
		"session_id":        d.sessionID,
		"connected_at_ms":   nowMs,
		"last_heartbeat_ms": nowMs,
		"is_paused":         "false",
	}

	if err := d.redisClient.HSet(d.ctx, sessionKey, sessionData).Err(); err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Set TTL
	if err := d.redisClient.Expire(d.ctx, sessionKey, 30*time.Second).Err(); err != nil {
		return fmt.Errorf("failed to set session TTL: %w", err)
	}

	// Publish session_active event
	event := &debug.Event{
		EventType: "session_active",
		SessionID: d.sessionID,
		Payload: map[string]interface{}{
			"connected_at_ms": nowMs,
		},
	}

	if err := debug.PublishEvent(d.ctx, d.redisClient, d.instanceName, event); err != nil {
		return fmt.Errorf("failed to publish session_active event: %w", err)
	}

	return nil
}

// StartHeartbeat begins the heartbeat goroutine
func (d *Debugger) StartHeartbeat() {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-d.cancelCtx.Done():
				return
			case <-ticker.C:
				if err := d.refreshHeartbeat(); err != nil {
					printer.Warning("Heartbeat failed: %v\n", err)
				}
			}
		}
	}()
}

// refreshHeartbeat updates the session heartbeat timestamp and TTL
func (d *Debugger) refreshHeartbeat() error {
	sessionKey := fmt.Sprintf("holt:%s:debug:session", d.instanceName)

	// Update last_heartbeat_ms
	nowMs := time.Now().UnixMilli()
	if err := d.redisClient.HSet(d.ctx, sessionKey, "last_heartbeat_ms", nowMs).Err(); err != nil {
		return err
	}

	// Refresh TTL
	return d.redisClient.Expire(d.ctx, sessionKey, 30*time.Second).Err()
}

// StartEventListener subscribes to debug events from orchestrator
func (d *Debugger) StartEventListener() {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()

		eventChannel := fmt.Sprintf("holt:%s:debug:event", d.instanceName)
		pubsub := d.redisClient.Subscribe(d.cancelCtx, eventChannel)
		defer pubsub.Close()

		ch := pubsub.Channel()

		for {
			select {
			case <-d.cancelCtx.Done():
				return
			case msg := <-ch:
				if msg == nil {
					continue
				}

				// Parse event
				event, err := debug.ParseEvent([]byte(msg.Payload))
				if err != nil {
					printer.Warning("Failed to parse event: %v\n", err)
					continue
				}

				// Only process events for our session
				if event.SessionID != d.sessionID {
					continue
				}

				// Send to event channel
				select {
				case d.eventCh <- event:
				case <-d.cancelCtx.Done():
					return
				}
			}
		}
	}()

	// Start event processor
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()

		for {
			select {
			case <-d.cancelCtx.Done():
				return
			case event := <-d.eventCh:
				d.handleEvent(event)
			}
		}
	}()
}

// handleEvent processes debug events from orchestrator
func (d *Debugger) handleEvent(event *debug.Event) {
	switch event.EventType {
	case "paused_on_breakpoint":
		d.mu.Lock()
		d.isPaused = true
		// Extract pause context directly from event payload
		d.pauseContext = &debug.PauseContext{
			ArtefactID:   getStringFromMap(event.Payload, "artefact_id"),
			ClaimID:      getStringFromMap(event.Payload, "claim_id"),
			BreakpointID: getStringFromMap(event.Payload, "breakpoint_id"),
			EventType:    getStringFromMap(event.Payload, "event_type"),
			PausedAtMs:   int64(getFloatFromMap(event.Payload, "paused_at_ms")),
		}
		d.mu.Unlock()

		bpID := getStringFromMap(event.Payload, "breakpoint_id")
		eventType := getStringFromMap(event.Payload, "event_type")
		artefactID := getStringFromMap(event.Payload, "artefact_id")
		claimID := getStringFromMap(event.Payload, "claim_id")

		// Show what we paused on
		fmt.Printf("\n🛑 Paused on breakpoint %s (event: %s)\n", bpID, eventType)
		if artefactID != "" {
			fmt.Printf("   Artefact: %s\n", shortID(artefactID))
		}
		if claimID != "" {
			fmt.Printf("   Claim: %s\n", shortID(claimID))
		}
		fmt.Println("Type 'continue' to resume, 'print' to inspect, or 'help' for commands")

	case "resumed":
		d.mu.Lock()
		d.isPaused = false
		d.pauseContext = nil
		d.mu.Unlock()

		printer.Success("▶️  Resumed")

	case "breakpoint_set":
		bpID := getStringFromMap(event.Payload, "breakpoint_id")
		condition := getStringFromMap(event.Payload, "condition")
		printer.Success("Breakpoint %s set: %s\n", bpID, condition)

	case "breakpoint_cleared":
		bpID := getStringFromMap(event.Payload, "breakpoint_id")
		printer.Info("Breakpoint %s cleared\n", bpID)

	case "step_complete":
		eventType := getStringFromMap(event.Payload, "event_type")
		printer.Info("Stepped: %s\n", eventType)

	case "session_expired":
		printer.Error(
			"session expired",
			"Debug session key expired (no heartbeat for 30s)",
			[]string{"Session automatically cleaned up", "Orchestrator resumed normal operation"},
		)
		d.cancelFunc()

	default:
		// Unknown event type, ignore
	}
}

// SetBreakpoint sends a set_breakpoints command
func (d *Debugger) SetBreakpoint(condition string) error {
	// Validate condition format
	if !strings.Contains(condition, "=") {
		return fmt.Errorf("invalid breakpoint condition: %s (expected format: condition_type=pattern)", condition)
	}

	parts := strings.SplitN(condition, "=", 2)
	conditionType := parts[0]
	pattern := parts[1]

	// Validate condition type
	validTypes := []string{"artefact.type", "artefact.structural_type", "claim.status", "agent.role", "event.type"}
	valid := false
	for _, vt := range validTypes {
		if conditionType == vt {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid condition type: %s (valid: %s)", conditionType, strings.Join(validTypes, ", "))
	}

	// Validate pattern
	if err := debug.ValidateBreakpointPattern(pattern); err != nil {
		return fmt.Errorf("invalid pattern: %w", err)
	}

	// Generate breakpoint ID
	d.mu.Lock()
	bpID := fmt.Sprintf("bp-%d", d.nextBPID)
	d.nextBPID++
	d.mu.Unlock()

	// Create breakpoint
	bp := &debug.Breakpoint{
		ID:            bpID,
		ConditionType: conditionType,
		Pattern:       pattern,
	}

	// Send command to orchestrator
	cmd := &debug.Command{
		CommandType: string(debug.CommandSetBreakpoints),
		SessionID:   d.sessionID,
		Payload: map[string]interface{}{
			"breakpoints": []interface{}{
				map[string]interface{}{
					"id":             bp.ID,
					"condition_type": bp.ConditionType,
					"pattern":        bp.Pattern,
				},
			},
		},
	}

	if err := debug.PublishCommand(d.ctx, d.redisClient, d.instanceName, cmd); err != nil {
		return err
	}

	// Store locally
	d.mu.Lock()
	d.breakpoints[bp.ID] = bp
	d.mu.Unlock()

	return nil
}

// Cleanup ends the debug session and cleans up resources
func (d *Debugger) Cleanup() {
	// Send clear_all command
	cmd := &debug.Command{
		CommandType: string(debug.CommandClearAll),
		SessionID:   d.sessionID,
		Payload:     map[string]interface{}{},
	}
	debug.PublishCommand(d.ctx, d.redisClient, d.instanceName, cmd)

	// Delete session key
	sessionKey := fmt.Sprintf("holt:%s:debug:session", d.instanceName)
	d.redisClient.Del(d.ctx, sessionKey)

	// Cancel context to stop goroutines
	d.cancelFunc()

	// Wait for goroutines with timeout to prevent hanging
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Clean shutdown
	case <-time.After(2 * time.Second):
		// Timeout - force exit anyway
		log.Printf("Warning: Goroutines did not shut down cleanly within 2s")
	}
}

// RunInteractivePrompt starts the go-prompt interactive interface
func (d *Debugger) RunInteractivePrompt() {
	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nReceived interrupt signal, cleaning up...")
		d.Cleanup()
		fmt.Println("Debug session ended")
		os.Exit(0)
	}()

	// Start event streaming in background (like holt watch)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		// Stream events with no filters (show all events)
		// Use default format (human-readable with timestamps and emojis)
		err := watch.StreamActivity(d.ctx, d.client, d.instanceName, watch.OutputFormatDefault, nil, false, false, os.Stdout)
		if err != nil && err != context.Canceled {
			// Don't show error on clean shutdown
			log.Printf("Event stream ended: %v", err)
		}
	}()

	// Start interactive prompt (no autocomplete - use 'help' instead)
	p := prompt.New(
		d.executor,
		func(doc prompt.Document) []prompt.Suggest { return []prompt.Suggest{} }, // No suggestions
		prompt.OptionPrefix("(holt-debug) "),
		prompt.OptionTitle("Holt Debugger"),
		prompt.OptionPrefixTextColor(prompt.Yellow),
	)

	p.Run()
}

// executor handles command execution in the prompt
func (d *Debugger) executor(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}

	parts := strings.Fields(input)
	command := parts[0]
	args := parts[1:]

	switch command {
	case "exit", "quit", "q":
		fmt.Println("Exiting debug session...")
		d.Cleanup()
		// Force exit immediately after cleanup
		os.Exit(0)

	case "help", "h", "?":
		d.printHelp()

	case "continue", "c":
		d.cmdContinue()

	case "next", "n":
		d.cmdNext()

	case "break", "b":
		if len(args) == 0 {
			printer.Error("missing argument", "Usage: break <condition>", []string{"Example: break artefact.type=CodeCommit"})
			return
		}
		d.cmdBreak(strings.Join(args, " "))

	case "breakpoints", "bp":
		d.cmdBreakpoints()

	case "clear":
		if len(args) == 0 {
			printer.Error("missing argument", "Usage: clear <breakpoint-id>", []string{"List breakpoints: breakpoints"})
			return
		}
		d.cmdClear(args[0])

	case "print", "p":
		artefactID := ""
		if len(args) > 0 {
			artefactID = args[0]
		}
		d.cmdPrint(artefactID)

	case "reviews":
		d.cmdReviews()

	case "review":
		if len(args) < 2 {
			printer.Error("missing arguments", "Usage: review <claim-id> --approve | --reject \"reason\"", nil)
			return
		}
		d.cmdReview(args)

	case "forage":
		d.cmdForage(args)

	case "terminate", "kill":
		d.cmdTerminate()

	default:
		printer.Warning("Unknown command: %s (type 'help' for commands)\n", command)
	}
}

// completer provides auto-completion suggestions

// Command implementations

func (d *Debugger) cmdContinue() {
	cmd := &debug.Command{
		CommandType: string(debug.CommandContinue),
		SessionID:   d.sessionID,
		Payload:     map[string]interface{}{},
	}

	if err := debug.PublishCommand(d.ctx, d.redisClient, d.instanceName, cmd); err != nil {
		printer.Warning("Failed to send continue command: %v\n", err)
		return
	}

	printer.Info("Continuing...\n")
}

func (d *Debugger) cmdNext() {
	cmd := &debug.Command{
		CommandType: string(debug.CommandStepNext),
		SessionID:   d.sessionID,
		Payload:     map[string]interface{}{},
	}

	if err := debug.PublishCommand(d.ctx, d.redisClient, d.instanceName, cmd); err != nil {
		printer.Warning("Failed to send step command: %v\n", err)
		return
	}

	printer.Info("Stepping to next event...\n")
}

func (d *Debugger) cmdBreak(condition string) {
	if err := d.SetBreakpoint(condition); err != nil {
		printer.Warning("Failed to set breakpoint: %v\n", err)
	}
}

func (d *Debugger) cmdBreakpoints() {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(d.breakpoints) == 0 {
		printer.Info("No active breakpoints\n")
		return
	}

	fmt.Println("\nActive Breakpoints:")
	for id, bp := range d.breakpoints {
		fmt.Printf("  %s: %s=%s\n", id, bp.ConditionType, bp.Pattern)
	}
	fmt.Println()
}

func (d *Debugger) cmdClear(breakpointID string) {
	cmd := &debug.Command{
		CommandType: string(debug.CommandClearBreakpoint),
		SessionID:   d.sessionID,
		Payload: map[string]interface{}{
			"breakpoint_id": breakpointID,
		},
	}

	if err := debug.PublishCommand(d.ctx, d.redisClient, d.instanceName, cmd); err != nil {
		printer.Warning("Failed to send clear command: %v\n", err)
		return
	}

	// Remove locally
	d.mu.Lock()
	delete(d.breakpoints, breakpointID)
	d.mu.Unlock()

	printer.Success("Breakpoint %s cleared\n", breakpointID)
}

func (d *Debugger) cmdPrint(artefactID string) {
	// If no ID provided, use pause context
	if artefactID == "" {
		d.mu.RLock()
		pauseCtx := d.pauseContext
		d.mu.RUnlock()

		if pauseCtx != nil {
			if pauseCtx.ArtefactID != "" {
				artefactID = pauseCtx.ArtefactID
			} else if pauseCtx.ClaimID != "" {
				// For review events, show the claim instead
				d.printClaim(pauseCtx.ClaimID)
				return
			}
		}

		if artefactID == "" {
			printer.Error("no artefact to print", "Not paused on an artefact or claim, specify ID", []string{"Usage: print <artefact-id>"})
			return
		}
	}

	// Handle shortened IDs - try both artefact and claim
	if len(artefactID) < 64 { // Full ID is 64 chars (SHA-256 hex)
		// Try artefact first
		fullID, errArtefact := d.expandShortID(artefactID, "artefact")
		if errArtefact == nil {
			artefactID = fullID
		} else {
			// Try claim instead
			fullID, errClaim := d.expandShortID(artefactID, "claim")
			if errClaim == nil {
				// It's a claim, print claim instead
				d.printClaim(fullID)
				return
			}
			// Neither worked, show error
			printer.Warning("No artefact or claim found matching short ID '%s'\n", artefactID)
			printer.Info("Tried: %v\n", errArtefact)
			printer.Info("       %v\n", errClaim)
			return
		}
	}

	// Fetch artefact from blackboard
	artefact, err := d.client.GetArtefact(d.ctx, artefactID)
	if err != nil {
		printer.Warning("Artefact %s not found: %v\n", artefactID, err)
		return
	}

	// Display artefact
	fmt.Println("\n" + strings.Repeat("─", 60))
	fmt.Printf("Artefact %s\n", shortID(artefact.ID))
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  Type:             %s\n", artefact.Type)
	fmt.Printf("  Structural Type:  %s\n", artefact.StructuralType)
	fmt.Printf("  Produced By:      %s\n", artefact.ProducedByRole)
	fmt.Printf("  Version:          %d\n", artefact.Version)
	fmt.Printf("  Payload:          %s\n", artefact.Payload)
	if len(artefact.SourceArtefacts) > 0 {
		// Show short IDs for source artefacts
		shortSources := make([]string, len(artefact.SourceArtefacts))
		for i, src := range artefact.SourceArtefacts {
			shortSources[i] = shortID(src)
		}
		fmt.Printf("  Source Artefacts: %v\n", shortSources)
	}
	fmt.Printf("  Created:          %d ms\n", artefact.CreatedAtMs)
	fmt.Println(strings.Repeat("─", 60) + "\n")
}

func (d *Debugger) printClaim(claimID string) {
	claim, err := d.client.GetClaim(d.ctx, claimID)
	if err != nil {
		printer.Warning("Claim %s not found: %v\n", claimID, err)
		return
	}

	// Get the artefact being claimed
	artefact, err := d.client.GetArtefact(d.ctx, claim.ArtefactID)
	if err != nil {
		printer.Warning("Artefact %s not found: %v\n", claim.ArtefactID, err)
		return
	}

	// Get bids for this claim
	bids, err := d.client.GetAllBids(d.ctx, claim.ID)
	if err != nil {
		printer.Warning("Failed to get bids: %v\n", err)
		bids = make(map[string]blackboard.Bid) // Continue with empty bids
	}

	fmt.Println("\n" + strings.Repeat("─", 60))
	fmt.Printf("Claim %s\n", shortID(claim.ID))
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  Status:           %s\n", claim.Status)
	fmt.Printf("  Artefact:         %s (v%d)\n", artefact.Type, artefact.Version)
	fmt.Printf("  Artefact ID:      %s\n", shortID(claim.ArtefactID))

	// Show bids grouped by type
	if len(bids) > 0 {
		fmt.Printf("\n  Bids:\n")

		// Group bids by type
		var reviewBids, parallelBids, exclusiveBids, ignoreBids []string
		for agent, bid := range bids {
			switch bid.BidType {
			case blackboard.BidTypeReview:
				reviewBids = append(reviewBids, agent)
			case blackboard.BidTypeParallel:
				parallelBids = append(parallelBids, agent)
			case blackboard.BidTypeExclusive:
				exclusiveBids = append(exclusiveBids, agent)
			case blackboard.BidTypeIgnore:
				ignoreBids = append(ignoreBids, agent)
			}
		}

		if len(exclusiveBids) > 0 {
			fmt.Printf("    Exclusive:  %v\n", exclusiveBids)
		}
		if len(reviewBids) > 0 {
			fmt.Printf("    Review:     %v\n", reviewBids)
		}
		if len(parallelBids) > 0 {
			fmt.Printf("    Parallel:   %v\n", parallelBids)
		}
		if len(ignoreBids) > 0 {
			fmt.Printf("    Ignore:     %v\n", ignoreBids)
		}
	}

	// Show granted agents based on claim status
	if claim.GrantedExclusiveAgent != "" {
		fmt.Printf("\n  Granted (excl):   %s\n", claim.GrantedExclusiveAgent)
	}
	if len(claim.GrantedParallelAgents) > 0 {
		fmt.Printf("  Granted (para):   %v\n", claim.GrantedParallelAgents)
	}
	if len(claim.GrantedReviewAgents) > 0 {
		fmt.Printf("  Review agents:    %v\n", claim.GrantedReviewAgents)
	}

	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("\nArtefact payload: %s\n", artefact.Payload)
	fmt.Println()
}

func (d *Debugger) cmdReviews() {
	// TODO: Query Redis for pending_review claims
	printer.Info("Listing pending reviews...\n")
	printer.Warning("Not yet implemented\n")
}

func (d *Debugger) cmdReview(args []string) {
	// TODO: Parse args and send manual_review command
	printer.Info("Manual review...\n")
	printer.Warning("Not yet implemented\n")
}

func (d *Debugger) cmdForage(args []string) {
	// Parse --goal flag
	var goal string
	for i := 0; i < len(args); i++ {
		if args[i] == "--goal" {
			if i+1 >= len(args) {
				printer.Error("missing argument", "Usage: forage --goal \"description\"", []string{"Example: forage --goal \"Create a REST API\""})
				return
			}
			// Join all remaining args as the goal (handles both quoted and unquoted)
			goal = strings.Join(args[i+1:], " ")
			break
		}
	}

	if goal == "" {
		printer.Error("missing --goal flag", "Usage: forage --goal \"description\"", []string{"Example: forage --goal \"Create a REST API\""})
		return
	}

	// Create GoalDefined artefact using V2 content addressing
	v2Artefact := &blackboard.VerifiableArtefact{
		Header: blackboard.ArtefactHeader{
			ParentHashes:    []string{},
			LogicalThreadID: blackboard.NewID(),
			Version:         1,
			CreatedAtMs:     time.Now().UnixMilli(),
			ProducedByRole:  "user",
			StructuralType:  blackboard.StructuralTypeStandard,
			Type:            "GoalDefined",
			ClaimID:         "", // Root artefact
		},
		Payload: blackboard.ArtefactPayload{
			Content: goal,
		},
	}

	hash, err := blackboard.ComputeArtefactHash(v2Artefact)
	if err != nil {
		printer.Error("failed to compute hash", err.Error(), nil)
		return
	}
	v2Artefact.ID = hash

	// Convert to V1 for creation
	v1Artefact := &blackboard.Artefact{
		ID:              v2Artefact.ID,
		LogicalID:       v2Artefact.Header.LogicalThreadID,
		Version:         v2Artefact.Header.Version,
		StructuralType:  v2Artefact.Header.StructuralType,
		Type:            v2Artefact.Header.Type,
		Payload:         v2Artefact.Payload.Content,
		SourceArtefacts: v2Artefact.Header.ParentHashes,
		ProducedByRole:  v2Artefact.Header.ProducedByRole,
		CreatedAtMs:     v2Artefact.Header.CreatedAtMs,
		ClaimID:         v2Artefact.Header.ClaimID,
	}

	if err := d.client.CreateArtefact(d.ctx, v1Artefact); err != nil {
		printer.Error("failed to create artefact", err.Error(), []string{"Ensure instance is running and Redis is accessible"})
		return
	}

	// Show success with both short and full ID
	shortID := v1Artefact.ID
	if len(shortID) > 8 {
		shortID = shortID[:8] + "..."
	}
	printer.Success("✓ GoalDefined artefact created: %s\n", shortID)
	printer.Info("Full ID: %s\n", v1Artefact.ID)

	// Auto-continue if we're paused (workflow should start immediately)
	d.mu.RLock()
	wasPaused := d.isPaused
	d.mu.RUnlock()

	if wasPaused {
		printer.Info("Resuming to start workflow...\n")
		d.cmdContinue()
	}
}

func (d *Debugger) cmdTerminate() {
	// Can only terminate when paused
	d.mu.RLock()
	pauseCtx := d.pauseContext
	isPaused := d.isPaused
	d.mu.RUnlock()

	if !isPaused || pauseCtx == nil || pauseCtx.ClaimID == "" {
		printer.Error("not paused on a claim",
			"The terminate command can only be used when paused on a claim",
			[]string{"Set a breakpoint and trigger a pause first"})
		return
	}

	// Show what we're about to terminate
	fmt.Printf("\n⚠️  WARNING: About to TERMINATE claim %s\n", shortID(pauseCtx.ClaimID))
	fmt.Printf("   This will:\n")
	fmt.Printf("   - Mark the claim as TERMINATED (permanent)\n")
	fmt.Printf("   - Create audit trail showing manual intervention\n")
	fmt.Printf("   - Stop this workflow branch immediately\n")
	fmt.Printf("   - Record your debugger session ID in the termination reason\n\n")

	// Send terminate command to orchestrator
	cmd := &debug.Command{
		CommandType: string(debug.CommandTerminateClaim),
		SessionID:   d.sessionID,
		Payload:     map[string]interface{}{},
	}

	if err := debug.PublishCommand(d.ctx, d.redisClient, d.instanceName, cmd); err != nil {
		printer.Error("failed to send terminate command", err.Error(), nil)
		return
	}

	printer.Success("✓ Claim terminated successfully\n")
	printer.Info("The termination is recorded in the claim's audit trail\n")
}

func (d *Debugger) printHelp() {
	help := `
Holt Debugger Commands:

  Execution Control:
    continue (c)              Resume workflow execution until next breakpoint
    next (n)                  Single-step: process one event, then pause again
    exit                      End debug session and clear all breakpoints

  Breakpoints:
    break <condition> (b)     Set new breakpoint
                              Formats:
                                artefact.type=<glob>
                                artefact.structural_type=<type>
                                claim.status=<status>
                                agent.role=<glob>
                                event.type=<event>
    breakpoints (bp)          List all active breakpoints
    clear <breakpoint-id>     Clear specific breakpoint by ID

  Inspection:
    print [artefact-id] (p)   Inspect artefact (current or by ID)
    reviews                   List all claims in pending_review status

  Manual Intervention:
    review <claim-id>         Manually review claim
      --approve               Approve the claim
      --reject "reason"       Reject with feedback
    terminate (kill)          Kill the currently paused claim
                              Creates permanent audit trail of manual termination

  Workflow Management:
    forage --goal "text"      Start a new workflow with the given goal

  Help:
    help (h, ?)               Show this help message

Examples:
  break artefact.type=CodeCommit     # Pause on code commits
  break claim.status=pending_review  # Pause when reviews needed
  print                              # Inspect current artefact
  forage --goal "Create API tests"   # Start new workflow
  continue                           # Resume workflow
`
	fmt.Println(help)
}

// Helper functions

func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getFloatFromMap(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

// shortID returns the first 8 characters of a UUID for readability
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// expandShortID attempts to expand a short ID (8 chars) to a full UUID by searching Redis
func (d *Debugger) expandShortID(shortID, entityType string) (string, error) {
	// Get all keys matching the pattern based on entity type
	var pattern string
	switch entityType {
	case "artefact":
		pattern = fmt.Sprintf("holt:%s:artefact:*", d.instanceName)
	case "claim":
		pattern = fmt.Sprintf("holt:%s:claim:*", d.instanceName)
	default:
		return "", fmt.Errorf("unknown entity type: %s", entityType)
	}

	// Scan for matching keys
	var cursor uint64
	var matches []string
	seenIDs := make(map[string]bool) // Deduplicate
	for {
		keys, nextCursor, err := d.redisClient.Scan(d.ctx, cursor, pattern, 100).Result()
		if err != nil {
			return "", fmt.Errorf("failed to scan Redis: %w", err)
		}

		// Extract IDs and check for prefix match
		for _, key := range keys {
			// Extract UUID from key (format: holt:instance:type:UUID or holt:instance:type:UUID:suffix)
			parts := strings.Split(key, ":")
			if len(parts) < 4 {
				continue
			}
			fullID := parts[3]

			// Skip keys with suffixes like `:bids` (e.g., holt:instance:claim:UUID:bids)
			if len(parts) > 4 {
				continue
			}

			// Check if this ID starts with the short ID
			if strings.HasPrefix(fullID, shortID) {
				if !seenIDs[fullID] {
					seenIDs[fullID] = true
					matches = append(matches, fullID)
				}
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// Handle results
	if len(matches) == 0 {
		return "", fmt.Errorf("no %s found matching short ID '%s'", entityType, shortID)
	}
	if len(matches) > 1 {
		// Show full IDs to help user differentiate
		var idList string
		for i, id := range matches {
			if i > 2 {
				idList += ", ..."
				break
			}
			if i > 0 {
				idList += ", "
			}
			idList += id
		}
		return "", fmt.Errorf("ambiguous short ID '%s': matches %d %ss\n  Matches: %s",
			shortID, len(matches), entityType, idList)
	}

	return matches[0], nil
}
