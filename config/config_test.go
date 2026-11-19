package config

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ti/common-go/grpcmux"
	"github.com/ti/common-go/log"
)

type testConfig struct {
	Name string
	Log  struct {
		Level string
	}
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A config struct with a Log field and a file without log.level used to panic
// in Init (log.SetLevel("")).
func TestInitWithoutLogLevel(t *testing.T) {
	defer log.SetLevel("debug")
	log.SetLevel("warn")
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "name: a\n")
	var cfg testConfig
	if err := Init(context.Background(), path, &cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if cfg.Name != "a" {
		t.Fatalf("name = %q", cfg.Name)
	}
	if log.Level() != slog.LevelWarn {
		t.Fatalf("an absent log.level changed the level to %v", log.Level())
	}
}

// An unknown level name is a configuration error at startup, not a panic.
func TestInitRejectsUnknownLogLevel(t *testing.T) {
	defer log.SetLevel("debug")
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "log:\n  level: verbose\n")
	var cfg testConfig
	err := Init(context.Background(), path, &cfg)
	if err == nil || !strings.Contains(err.Error(), "verbose") {
		t.Fatalf("Init = %v, want an error naming the level", err)
	}
}

// A reload applies a valid level and survives an invalid one (it used to
// panic in the watcher goroutine and kill the process).
func TestReloadLogLevel(t *testing.T) {
	defer log.SetLevel("debug")
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "log:\n  level: info\n")
	var cfg testConfig
	if err := Init(context.Background(), path, &cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if log.Level() != slog.LevelInfo {
		t.Fatalf("level = %v, want INFO", log.Level())
	}
	// The file watcher applies changes; wait for each one.
	reload := func(body string, want slog.Level) {
		t.Helper()
		writeConfig(t, path, body)
		deadline := time.Now().Add(10 * time.Second)
		for log.Level() != want {
			if time.Now().After(deadline) {
				t.Fatalf("level = %v after writing %q, want %v", log.Level(), body, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	reload("log:\n  level: error\n", slog.LevelError)
	// An unknown name is logged and ignored; the watcher goroutine survives
	// and applies the next valid value.
	writeConfig(t, path, "log:\n  level: nonsense\n")
	time.Sleep(300 * time.Millisecond)
	if log.Level() != slog.LevelError {
		t.Fatalf("level after an invalid reload = %v, want ERROR (unchanged)", log.Level())
	}
	reload("log:\n  level: warn\n", slog.LevelWarn)
}

// The server options added to grpcmux.Config bind from YAML, including the
// nested CORS block (it had no tags, so consumers hard-coded it).
func TestServerConfigBindsFromYAML(t *testing.T) {
	defer log.SetLevel("debug")
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, `
apis:
  httpAddr: ":18080"
  reflection: true
  drainStreams: true
  drainStreamsGrace: 5s
  protocolErrors: true
  disableQueryAccessToken: true
  disableMethodOverride: true
  debugHandlers: "false"
  noAuthMethods:
    - /auth.v1.AuthService/Token
    - /public.v1.PublicService/*
  cors:
    allowedOrigins:
      - https://app.example.com
    allowedHeaders:
      - X-Tenant
    exposeHeaders:
      - X-Total-Count
`)
	var cfg struct {
		Apis grpcmux.Config
	}
	if err := Init(context.Background(), path, &cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}
	a := cfg.Apis
	if a.HTTPAddr != ":18080" || !a.Reflection || !a.DrainStreams || a.DrainStreamsGrace != "5s" ||
		!a.ProtocolErrors || !a.DisableQueryAccessToken || !a.DisableMethodOverride || a.DebugHandlers != "false" {
		t.Errorf("apis = %+v", a)
	}
	if len(a.NoAuthMethods) != 2 || a.NoAuthMethods[1] != "/public.v1.PublicService/*" {
		t.Errorf("noAuthMethods = %v", a.NoAuthMethods)
	}
	if a.CORS == nil || len(a.CORS.AllowedOrigins) != 1 || a.CORS.AllowedOrigins[0] != "https://app.example.com" ||
		len(a.CORS.AllowedHeaders) != 1 || len(a.CORS.ExposeHeaders) != 1 || a.CORS.Disabled {
		t.Errorf("cors = %+v", a.CORS)
	}
	// The values are accepted by the server.
	_ = grpcmux.NewServer(grpcmux.WithConfig(&a), grpcmux.WithMetricsAddr(""))
}

// Log may be a pointer to a struct: the level is applied and hot-reloaded,
// as it is for a struct field (and as it was before log.level became
// optional).
func TestLogLevelWithPointerLogField(t *testing.T) {
	defer log.SetLevel("debug")
	type logConfig struct{ Level string }
	for name, body := range map[string]string{"set": "log:\n  level: warn\n", "absent": "name: a\n"} {
		t.Run(name, func(t *testing.T) {
			log.SetLevel("info")
			path := filepath.Join(t.TempDir(), "config.yaml")
			writeConfig(t, path, body)
			var cfg struct {
				Name string
				Log  *logConfig
			}
			if err := Init(context.Background(), path, &cfg); err != nil {
				t.Fatalf("Init: %v", err)
			}
			// Init does not change the shape of the caller's struct: without
			// a log key the pointer stays nil.
			if (cfg.Log == nil) != (name == "absent") {
				t.Fatalf("cfg.Log = %v after Init with log %s", cfg.Log, name)
			}
			want := slog.LevelInfo
			if name == "set" {
				want = slog.LevelWarn
			}
			if log.Level() != want {
				t.Fatalf("level = %v, want %v", log.Level(), want)
			}
			// A later change is applied in both cases.
			writeConfig(t, path, "log:\n  level: error\n")
			deadline := time.Now().Add(10 * time.Second)
			for log.Level() != slog.LevelError {
				if time.Now().After(deadline) {
					t.Fatalf("level = %v after the reload, want ERROR", log.Level())
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

// A Log field without a string Level is the application's own: Init leaves
// it alone (it used to panic).
func TestLogFieldWithoutLevelIsIgnored(t *testing.T) {
	defer log.SetLevel("debug")
	log.SetLevel("info")
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "log:\n  file: /tmp/x\n")
	var cfg struct {
		Log struct{ File string }
	}
	if err := Init(context.Background(), path, &cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if cfg.Log.File != "/tmp/x" || log.Level() != slog.LevelInfo {
		t.Fatalf("file = %q, level = %v", cfg.Log.File, log.Level())
	}
}

// Level may be a named string type: it is applied at Init and on reload (the
// reload used to be ignored for such a type).
func TestLogLevelOfNamedStringType(t *testing.T) {
	defer log.SetLevel("debug")
	type level string
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "log:\n  level: warn\n")
	var cfg struct {
		Log struct{ Level level }
	}
	if err := Init(context.Background(), path, &cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if log.Level() != slog.LevelWarn {
		t.Fatalf("level = %v, want WARN", log.Level())
	}
	writeConfig(t, path, "log:\n  level: error\n")
	deadline := time.Now().Add(10 * time.Second)
	for log.Level() != slog.LevelError {
		if time.Now().After(deadline) {
			t.Fatalf("level = %v after the reload, want ERROR", log.Level())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
