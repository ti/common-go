package sql

import (
	"context"
	"net/url"
	"testing"
)

// Package options (log, compact, bustedIndex, updateTime) are read and then
// removed from the DSN passed to the driver. log= used to be ignored.
func TestInitReadsPackageOptions(t *testing.T) {
	u, _ := url.Parse("postgres://u:p@127.0.0.1:1/db?sslmode=disable&log=debug&compact=1&updateTime=updated_at&connect_timeout=1")
	s := &SQL{}
	_ = s.Init(context.Background(), u) // the ping fails: nothing listens on :1
	if s.logMode != "debug" || !s.compactMode || !s.updateDifferent {
		t.Fatalf("logMode=%q compact=%v updateDifferent=%v", s.logMode, s.compactMode, s.updateDifferent)
	}
	q := u.Query()
	for _, k := range []string{"log", "compact", "updateTime"} {
		if q.Has(k) {
			t.Errorf("%s left in the driver DSN: %s", k, u.RawQuery)
		}
	}
	if q.Get("sslmode") != "disable" {
		t.Errorf("driver option dropped: %s", u.RawQuery)
	}
}

func TestInvalidLogMode(t *testing.T) {
	u, _ := url.Parse("postgres://u:p@127.0.0.1:1/db?log=verbose")
	if err := (&SQL{}).Init(context.Background(), u); err == nil {
		t.Fatal("want an error for log=verbose")
	}
}
