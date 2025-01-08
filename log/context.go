package log

import (
	"context"
)

type loggerKey struct{}

// NewContext context with tags logger
func NewContext(ctx context.Context, tags map[string]any) context.Context {
	return context.WithValue(ctx, loggerKey{}, std.newWithTags(tags))
}

// NewContextWithLogger context with tags logger
func NewContextWithLogger(ctx context.Context, logger Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// NewOrFromContext context with tags logger
func NewOrFromContext(ctx context.Context, logger Logger) context.Context {
	if _, ok := ctx.Value(loggerKey{}).(Logger); ok {
		return ctx
	}
	return context.WithValue(ctx, loggerKey{}, logger)
}

// Inject add tags to current context
func Inject(ctx context.Context, tags map[string]any) {
	if ctxLogger, ok := ctx.Value(loggerKey{}).(Logger); ok {
		ctxLogger.Inject(tags)
	}
}

// Extract logger from context
func Extract(ctx context.Context) Logger {
	if ctx == nil {
		return std
	}
	if ctxLogger, ok := ctx.Value(loggerKey{}).(Logger); ok {
		return ctxLogger
	}
	return std
}
