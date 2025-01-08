package log

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"sort"
)

var (
	std *sLogger
)

const actionKey = "action"

func init() {
	lvl := new(slog.LevelVar)
	lvl.Set(slog.LevelDebug)
	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
	})
	logger := slog.New(logHandler)
	slog.SetDefault(logger)
	std = &sLogger{
		logger: logger,
		level:  lvl,
	}
}

type sLogger struct {
	logger *slog.Logger
	level  *slog.LevelVar
	fields []any
}

// Debug logs a message at DebugLevel. The message includes any fields passed
// at the log site, as well as any fields accumulated on the logger.
func (l *sLogger) Debug(msgOrFormat string, args ...any) {
	if len(args) == 0 {
		l.logger.Debug(msgOrFormat)
		return
	}
	l.logger.Debug(fmt.Sprintf(msgOrFormat, args...))
}

// Info logs a message at InfoLevel. The message includes any fields passed
// at the log site, as well as any fields accumulated on the logger.
func (l *sLogger) Info(msgOrFormat string, args ...any) {
	if len(args) == 0 {
		l.logger.Info(msgOrFormat)
		return
	}
	l.logger.Info(fmt.Sprintf(msgOrFormat, args...))
}

// Warn logs a message at WarnLevel. The message includes any fields passed
// at the log site, as well as any fields accumulated on the logger.
func (l *sLogger) Warn(msgOrFormat string, args ...any) {
	if len(args) == 0 {
		l.logger.Warn(msgOrFormat)
		return
	}
	l.logger.Warn(fmt.Sprintf(msgOrFormat, args...))
}

// Error logs a message at ErrorLevel. The message includes any fields passed
// at the log site, as well as any fields accumulated on the logger.
func (l *sLogger) Error(msgOrFormat string, args ...any) {
	if len(args) == 0 {
		l.logger.Error(msgOrFormat)
		return
	}
	l.logger.Error(fmt.Sprintf(msgOrFormat, args...))
}

// Fatal logs a message at FatalLevel. The message includes any fields passed
// at the log site, as well as any fields accumulated on the logger.
//
// The logger then calls os.Exit(1), even if logging at FatalLevel is
// disabled.
func (l *sLogger) Fatal(msgOrFormat string, args ...any) {
	if len(args) == 0 {
		log.Fatalln(msgOrFormat)
		return
	}
	log.Fatalln(fmt.Sprintf(msgOrFormat, args...))
}

// Action logger with just an action key.
func (l *sLogger) Action(action string) StdLogger {
	lenInternal := len(l.fields)
	fields := make([]any, lenInternal+1)
	copy(fields, l.fields)
	fields[lenInternal] = slog.String(actionKey, action)
	return &sLogger{
		logger: l.logger.With(fields...),
		level:  l.level,
	}
}

// With add custom maps for logger
func (l *sLogger) With(m map[string]any) StdLogger {
	lenInternal := len(l.fields)
	fields := make([]any, lenInternal+len(m))
	copy(fields, l.fields)
	withFields := tagsToFields(m)
	for i, v := range withFields {
		fields[lenInternal+i] = v
	}
	return &sLogger{
		logger: l.logger.With(fields...),
		level:  l.level,
	}
}

func tagsToFields(m map[string]any) []any {
	lenMap := len(m)
	if lenMap == 0 {
		return nil
	}
	fields := make([]any, lenMap)
	keys := make([]string, 0)
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, key := range keys {
		value := m[key]
		switch v := value.(type) {
		case string:
			fields[i] = slog.String(key, v)
		case int:
			fields[i] = slog.Int(key, v)
		case int32:
			fields[i] = slog.Int(key, int(v))
		case int64:
			fields[i] = slog.Int64(key, v)
		case bool:
			fields[i] = slog.Bool(key, v)
		case float32:
			fields[i] = slog.Float64(key, float64(v))
		case float64:
			fields[i] = slog.Float64(key, v)
		default:
			fields[i] = slog.Any(key, v)
		}
	}
	return fields
}

// newWithTags new logger with tags
func (l *sLogger) newWithTags(m map[string]any) Logger {
	return &sLogger{
		logger: l.logger.With(tagsToFields(m)...),
		level:  l.level,
	}
}

// Inject inject data
func (l *sLogger) Inject(m map[string]any) {
	fields := tagsToFields(m)
	l.fields = append(l.fields, fields...)
}
