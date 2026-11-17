package main

import (
	"fmt"
	"io"
	"log/slog"
)

// newLogger builds the process logger from log_level (debug|info|warn|error)
// and log_format (text|json). Unknown values are errors (config.Validate
// normally catches them first).
func newLogger(level, format string, w io.Writer) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log_level %q", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("invalid log_format %q", format)
	}
}
