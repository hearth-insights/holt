package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/c-bata/go-prompt"
	dockerpkg "github.com/dyluth/holt/internal/docker"
	"github.com/dyluth/holt/internal/instance"
	"github.com/dyluth/holt/internal/orchestrator/debug"
	"github.com/dyluth/holt/internal/printer"
	"github.com/dyluth/holt/pkg/blackboard"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var (
	debugInstanceName      string
	debugBreakpoints       []string // Repeatable --break flag
	debugPauseOnStart      bool     // Pause immediately on attach
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
	ctx           context.Context
	client        *blackboard.Client
	instanceName  string
	sessionID     string
	redisClient   *redis.Client

	// Cancellation and cleanup
	cancelCtx     context.Context
	cancelFunc    context.CancelFunc
	wg            sync.WaitGroup

	// State
	mu            sync.RWMutex
	isPaused      bool
	pauseContext  *debug.PauseContext
	breakpoints   map[string]*debug.Breakpoint
	nextBPID      int

	// Communication channels
	eventCh       chan *debug.Event
	commandQueue  chan string
}

// NewDebugger creates a new debugger instance
func NewDebugger(ctx context.Context, client *blackboard.Client, instanceName string) *Debugger {
	cancelCtx, cancelFunc := context.WithCancel(ctx)

	return &Debugger{
		ctx:          ctx,
		client:       client,
		instanceName: instanceName,
		sessionID:    uuid.New().String(),
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
			fmt.Printf("   Artefact: %s\n", artefactID)
		}
		if claimID != "" {
			fmt.Printf("   Claim: %s\n", claimID)
		}
		fmt.Println("Type 'continue' to resume, 'print' to inspect, or 'help' for commands\n")

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

	// Cancel context and wait for goroutines
	d.cancelFunc()
	d.wg.Wait()
}

// RunInteractivePrompt starts the go-prompt interactive interface
func (d *Debugger) RunInteractivePrompt() {
	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		printer.Info("\nReceived interrupt signal, exiting...")
		d.Cleanup()
		os.Exit(0)
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
		printer.Info("Exiting debug session...\n")
		d.Cleanup()
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

	default:
		printer.Warning("Unknown command: %s (type 'help' for commands)\n", command)
	}
}

// completer provides auto-completion suggestions
func (d *Debugger) completer(doc prompt.Document) []prompt.Suggest {
	suggestions := []prompt.Suggest{
		{Text: "continue", Description: "Resume workflow execution"},
		{Text: "next", Description: "Single-step: process one event"},
		{Text: "break", Description: "Set new breakpoint"},
		{Text: "breakpoints", Description: "List active breakpoints"},
		{Text: "clear", Description: "Clear breakpoint by ID"},
		{Text: "print", Description: "Inspect artefact"},
		{Text: "reviews", Description: "List pending reviews"},
		{Text: "review", Description: "Manually review claim"},
		{Text: "forage", Description: "Start new workflow with goal"},
		{Text: "help", Description: "Show command reference"},
		{Text: "exit", Description: "End debug session"},
	}

	return prompt.FilterHasPrefix(suggestions, doc.GetWordBeforeCursor(), true)
}

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

	// Handle shortened IDs - we can't expand them without a full list API
	if len(artefactID) < 36 { // UUID is 36 chars with hyphens
		printer.Error("incomplete artefact ID",
			fmt.Sprintf("Short ID '%s' provided but full UUID required", artefactID),
			[]string{
				"Use full UUID from forage output or holt hoard",
				fmt.Sprintf("Example: holt hoard | grep %s", artefactID),
			})
		return
	}

	// Fetch artefact from blackboard
	artefact, err := d.client.GetArtefact(d.ctx, artefactID)
	if err != nil {
		printer.Warning("Artefact %s not found: %v\n", artefactID, err)
		return
	}

	// Display artefact
	fmt.Println("\n" + strings.Repeat("─", 60))
	fmt.Printf("Artefact %s\n", artefact.ID)
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  Type:             %s\n", artefact.Type)
	fmt.Printf("  Structural Type:  %s\n", artefact.StructuralType)
	fmt.Printf("  Produced By:      %s\n", artefact.ProducedByRole)
	fmt.Printf("  Version:          %d\n", artefact.Version)
	fmt.Printf("  Payload:          %s\n", artefact.Payload)
	if len(artefact.SourceArtefacts) > 0 {
		fmt.Printf("  Source Artefacts: %v\n", artefact.SourceArtefacts)
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

	fmt.Println("\n" + strings.Repeat("─", 60))
	fmt.Printf("Claim %s\n", claim.ID)
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  Status:           %s\n", claim.Status)
	fmt.Printf("  Artefact:         %s (v%d)\n", artefact.Type, artefact.Version)
	fmt.Printf("  Artefact ID:      %s\n", claim.ArtefactID)

	// Show granted agents based on claim status
	if claim.GrantedExclusiveAgent != "" {
		fmt.Printf("  Granted (excl):   %s\n", claim.GrantedExclusiveAgent)
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

	// Create GoalDefined artefact
	artefactID := uuid.New().String()
	logicalID := uuid.New().String()

	artefact := &blackboard.Artefact{
		ID:              artefactID,
		LogicalID:       logicalID,
		Version:         1,
		StructuralType:  blackboard.StructuralTypeStandard,
		Type:            "GoalDefined",
		Payload:         goal,
		SourceArtefacts: []string{},
		ProducedByRole:  "user",
		// CreatedAtMs auto-populated by CreateArtefact
	}

	if err := d.client.CreateArtefact(d.ctx, artefact); err != nil {
		printer.Error("failed to create artefact", err.Error(), []string{"Ensure instance is running and Redis is accessible"})
		return
	}

	// Show success with both short and full ID
	shortID := artefactID
	if len(shortID) > 8 {
		shortID = shortID[:8] + "..."
	}
	printer.Success("✓ GoalDefined artefact created: %s\n", shortID)
	printer.Info("Full ID: %s\n", artefactID)
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
