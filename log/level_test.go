package log

import (
	"log/slog"
	"testing"
)

// SetLevel used to panic on an empty or unknown name, which crashed startup
// (config with no log.level) and hot reloads (a typo in the new value).
func TestSetLevelNeverPanics(t *testing.T) {
	defer SetLevel("debug")
	SetLevel("warn")
	if Level() != slog.LevelWarn {
		t.Fatalf("level = %v, want WARN", Level())
	}
	SetLevel("") // optional: keeps the level
	SetLevel("verbose")
	if Level() != slog.LevelWarn {
		t.Fatalf("an empty or unknown name changed the level to %v", Level())
	}
	if err := TrySetLevel("verbose"); err == nil {
		t.Fatal("TrySetLevel(verbose) = nil, want an error")
	}
	if err := TrySetLevel(""); err != nil {
		t.Fatalf("TrySetLevel(\"\") = %v, want nil", err)
	}
	for name, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "INFO": slog.LevelInfo, "Warn": slog.LevelWarn,
		"warning": slog.LevelWarn, " error ": slog.LevelError,
	} {
		got, err := ParseLevel(name)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", name, got, err, want)
		}
	}
}
