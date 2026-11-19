// Package log implements log in golang.
package log

import (
	"fmt"
	"log/slog"
	"strings"
)

// Default the default log instance
func Default(withStacktrace bool) Logger {
	// TODO: set stacktrace
	return &sLogger{
		logger: std.logger,
		level:  std.level,
	}
}

// Action set action filed for logger
func Action(action string) StdLogger {
	return std.Action(action)
}

// With any map data, the value of key must be string, int ... basic value
func With(m map[string]any) StdLogger {
	return std.With(m)
}

// SetLevel sets the log level: debug, info, warn or error (any case; slog
// offsets such as "info+2" are accepted too). An empty name leaves the level
// unchanged. An unknown name leaves the level unchanged and is reported at
// error level; it never panics, so a typo in a hot-reloaded config cannot take
// the process down. Use [TrySetLevel] to handle the error yourself.
func SetLevel(level string) {
	if err := TrySetLevel(level); err != nil {
		std.Action("SetLogLevel").Error(err.Error())
	}
}

// TrySetLevel is [SetLevel] returning the error for an unknown level name
// instead of logging it. An empty name is not an error and changes nothing.
func TrySetLevel(level string) error {
	level = strings.TrimSpace(level)
	if level == "" {
		return nil
	}
	l, err := ParseLevel(level)
	if err != nil {
		return err
	}
	slog.SetLogLoggerLevel(l)
	std.level.Set(l)
	return nil
}

// ParseLevel parses a level name (debug, info, warn, error; any case).
func ParseLevel(level string) (slog.Level, error) {
	var l slog.Level
	name := strings.ToUpper(strings.TrimSpace(level))
	if name == "WARNING" {
		name = "WARN"
	}
	if err := l.UnmarshalText([]byte(name)); err != nil {
		return 0, fmt.Errorf("log: unknown level %q (want debug, info, warn or error)", level)
	}
	return l, nil
}

// Level returns the current log level.
func Level() slog.Level {
	return std.level.Level()
}
