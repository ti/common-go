package mux

import "testing"

func TestMatchPath(t *testing.T) {
	patterns := []string{"/auth.v1.AuthService/Token", "/note.v1.NoteService/*", "/healthz", "/static/*"}
	for path, want := range map[string]bool{
		"/auth.v1.AuthService/Token":       true,
		"/auth.v1.AuthService/TokenAdmin":  false, // a prefix match would exempt this
		"/auth.v1.AuthService/Token/extra": false,
		"/auth.v1.AuthService/Authorize":   false,
		"/note.v1.NoteService/ListNotes":   true,
		"/note.v1.NoteService/Watch/42":    true,
		"/note.v1.NoteService/":            false,
		"/note.v1.NoteServiceAdmin/Delete": false, // not the same segment
		"/note.v1.NoteService":             false,
		"/healthz":                         true,
		"/healthzz":                        false,
		"/static/app.js":                   true,
		"/staticfiles/app.js":              false,
		"":                                 false,
	} {
		if got := MatchPath(patterns, path); got != want {
			t.Errorf("MatchPath(%q) = %v, want %v", path, got, want)
		}
	}
	if MatchPath(nil, "/anything") {
		t.Error("no patterns must match nothing")
	}
	// A bare "*" suffix without a segment boundary is not a pattern.
	if MatchPath([]string{"/auth.v1.AuthService/Token*"}, "/auth.v1.AuthService/TokenAdmin") {
		t.Error(`"Token*" must not act as a prefix`)
	}
}
