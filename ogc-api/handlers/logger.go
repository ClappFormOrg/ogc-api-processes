package handlers

import (
	"log/slog"
	"ogc-api/processes/plotbag"
	"ogc-api/processes/plotbag/pdok"
)

// Logger is the shared structured logger for all handlers.
// Initialised in main via handlers.SetLogger.
var Logger *slog.Logger = slog.Default()

// SetLogger replaces the package logger (call once from main).
func SetLogger(l *slog.Logger) {
	Logger = l
	plotbag.Logger = l
	pdok.Logger = l
}
