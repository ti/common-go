// Package log implements log in golang.
package log

import (
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

// SetLevel set the log level with: debug, info, warn, error
func SetLevel(level string) {
	var l slog.Level
	err := l.UnmarshalText([]byte(strings.ToUpper(level)))
	if err != nil {
		panic(err)
	}
	slog.SetLogLoggerLevel(l)
	std.level.Set(l)
}
