package grpcmux

import (
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
)

// exemplarLabels builds the label set of a Prometheus exemplar from request
// metadata, within what the client library accepts.
//
// An exemplar may carry at most prometheus.ExemplarMaxRunes runes, counting
// label names and values together, and every value must be valid UTF-8;
// Counter.AddWithExemplar and Observer.ObserveWithExemplar PANIC otherwise.
// The values here come from request headers (device id, request id) and from
// the token (user id, client id), so a caller chooses their length: a signed-in
// request with a trace id, or any request with a long X-Device-Id, used to turn
// into a recovered panic and an Internal answer.
//
// traceID goes in first because it is what an exemplar is for (the jump from a
// metric to a trace). The fields follow in the order given, each only if it
// fits the remaining budget whole: a truncated identifier would look like a
// real one and match nothing. A value that is not valid UTF-8 is left out.
// The result is nil when nothing qualifies, which records no exemplar.
func exemplarLabels(traceID string, fields []string, value func(field string) string) prometheus.Labels {
	budget := prometheus.ExemplarMaxRunes
	var labels prometheus.Labels
	add := func(name, v string) {
		if v == "" || !utf8.ValidString(v) {
			return
		}
		cost := utf8.RuneCountInString(name) + utf8.RuneCountInString(v)
		if cost > budget {
			return
		}
		if labels == nil {
			labels = make(prometheus.Labels, len(fields)+1)
		}
		if _, dup := labels[name]; dup {
			return
		}
		labels[name] = v
		budget -= cost
	}
	add("trace_id", traceID)
	for _, f := range fields {
		if f == "trace_id" {
			continue
		}
		add(f, value(f))
	}
	return labels
}
