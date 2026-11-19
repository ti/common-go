package config

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ti/common-go/dependencies"
	"github.com/ti/common-go/dependencies/database"
	_ "github.com/ti/common-go/dependencies/database/mock"
)

type raceDeps struct {
	dependencies.Dependency
	A database.Database
	B database.Database
	C database.Database
	D database.Database
}

type raceConfig struct {
	Name         string
	Labels       map[string]string
	Dependencies raceDeps
}

const raceYAML = `name: a
dependencies:
  a: mock://local/a
  b: mock://local/b
  c: mock://local/c
  d: mock://local/d
`

// config.Init used to hand the caller's struct to the file watcher, which
// re-read and rewrote it (dependency fields included) while Init was still
// setting those fields, and afterwards while handlers used them. Run with
// -race: the loop made the old code fail on every run.
func TestInitDoesNotRaceWithTheWatcher(t *testing.T) {
	for range 30 {
		path := filepath.Join(t.TempDir(), "config.yaml")
		writeConfig(t, path, raceYAML)
		var cfg raceConfig
		if err := Init(context.Background(), path, &cfg, dependencies.WithNewFns(database.New)); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if cfg.Dependencies.D == nil || cfg.Name != "a" {
			t.Fatalf("cfg = %+v", cfg)
		}
	}
}

// A reload changes what the source owns and nothing else: dependency fields
// keep the clients Init created, also while goroutines use them, and a
// field read under RLock never races with the reload.
func TestReloadLeavesDependenciesAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, raceYAML)
	cfg := raceConfig{Labels: map[string]string{"default": "kept"}}
	if err := Init(context.Background(), path, &cfg, dependencies.WithNewFns(database.New)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// A value set before Init survives when the source has no such key.
	if cfg.Labels["default"] != "kept" {
		t.Fatalf("labels = %v, the default set before Init is gone", cfg.Labels)
	}
	a, d := cfg.Dependencies.A, cfg.Dependencies.D

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Handlers use dependencies without any lock...
				if cfg.Dependencies.A == nil || cfg.Dependencies.D == nil {
					t.Error("a dependency disappeared")
					return
				}
				_, _ = cfg.Dependencies.A.Count(context.Background(), "t", database.C{})
				// ...and reloadable fields under RLock.
				RLock()
				_ = cfg.Name
				_ = cfg.Labels["env"]
				RUnlock()
			}
		})
	}
	name := func() string {
		RLock()
		defer RUnlock()
		return cfg.Name
	}
	for i, next := range []string{"b", "c", "d"} {
		// The dependencies block changes too; it must be ignored.
		writeConfig(t, path, "name: "+next+"\nlabels:\n  env: e"+next+"\ndependencies:\n  a: mock://local/other"+next+"\n")
		deadline := time.Now().Add(10 * time.Second)
		for name() != next {
			if time.Now().After(deadline) {
				t.Fatalf("reload %d: name = %q, want %q", i, name(), next)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	close(stop)
	readers.Wait()
	RLock()
	defer RUnlock()
	if cfg.Labels["env"] != "ed" {
		t.Errorf("labels = %v after the reloads", cfg.Labels)
	}
	if cfg.Dependencies.A != a || cfg.Dependencies.D != d || cfg.Dependencies.B == nil {
		t.Error("a reload replaced or removed a dependency")
	}
}

func TestInitRejectsNonStruct(t *testing.T) {
	m := map[string]string{}
	if err := Init(context.Background(), filepath.Join(t.TempDir(), "c.yaml"), &m); err == nil {
		t.Fatal("Init with a pointer to a map: no error")
	}
}
