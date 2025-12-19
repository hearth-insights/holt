package debug

import (
	"log"
	"os"
	"sync"
)

var (
	debugEnabled     bool
	debugEnabledOnce sync.Once
)

// IsEnabled returns true if debug logging is enabled via HOLT_DEBUG environment variable.
// The check is cached after the first call for performance.
func IsEnabled() bool {
	debugEnabledOnce.Do(func() {
		debugEnabled = os.Getenv("HOLT_DEBUG") == "true" || os.Getenv("HOLT_DEBUG") == "1"
	})
	return debugEnabled
}

// Log prints a debug message if HOLT_DEBUG is enabled.
// Messages are prefixed with "[DEBUG]" for clarity.
func Log(format string, v ...interface{}) {
	if IsEnabled() {
		log.Printf("[DEBUG] "+format, v...)
	}
}
