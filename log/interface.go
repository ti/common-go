package log

// Logger is a logger that with action or map labels
type Logger interface {
	// Action logger with action key filed
	Action(action string) StdLogger
	// With any map data, the value of key must be string, int ... basic value
	With(map[string]any) StdLogger
	// Inject tags to current logger
	Inject(map[string]any)
}

// StdLogger the standard logger
type StdLogger interface {
	// Debug print the debug log if the len(args) is 0, the args will be ignored.
	Debug(msgOrFormat string, args ...any)
	Info(msgOrFormat string, args ...any)
	Warn(msgOrFormat string, args ...any)
	Error(msgOrFormat string, args ...any)
	Fatal(msgOrFormat string, args ...any)
}
