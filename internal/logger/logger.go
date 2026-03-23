// Package logger provides a shared slog.Logger for the whole binary.
// Call Init once at startup (in PersistentPreRunE) before any component uses Get.
package logger

import (
	"log/slog"
	"os"
)

var global *slog.Logger

// Init configures the global logger.
// debug=true → DEBUG level (all logs visible).
// debug=false → INFO level (only main-flow and error logs visible).
func Init(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	global = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(global)
}

// Get returns the global logger. Returns a default INFO logger if Init was not called.
func Get() *slog.Logger {
	if global == nil {
		Init(false)
	}
	return global
}
