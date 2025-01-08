package logging

import (
	"context"
	"encoding/json"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"google.golang.org/grpc/authz/audit"

	"google.golang.org/grpc/grpclog"
)

var grpcLogger = grpclog.Component("authz-audit")

// RegisterAuditLogger register audit logger
func RegisterAuditLogger(logger logging.Logger) {
	audit.RegisterLoggerBuilder(&loggerBuilder{
		goLogger: logger,
	})
}

// logger implements the audit.logger interface by logging to standard output.
type logger struct {
	goLogger logging.Logger
}

// Log marshals the audit.Event to json and prints it to standard output.
func (l *logger) Log(event *audit.Event) {
	l.goLogger.Log(context.Background(), logging.LevelInfo,
		"audit",
		convertEvent(event)...)
}

// loggerConfig represents the configuration for the stdout logger.
// It is currently empty and implements the audit.Logger interface by embedding it.
type loggerConfig struct {
	audit.LoggerConfig
}

type loggerBuilder struct {
	goLogger logging.Logger
}

// Name get log name
func (loggerBuilder) Name() string {
	return "logger"
}

// Build returns a new instance of the stdout logger.
// Passed in configuration is ignored as the stdout logger does not
// expect any configuration to be provided.
func (lb *loggerBuilder) Build(audit.LoggerConfig) audit.Logger {
	return &logger{
		goLogger: lb.goLogger,
	}
}

// ParseLoggerConfig is a no-op since the stdout logger does not accept any configuration.
func (*loggerBuilder) ParseLoggerConfig(config json.RawMessage) (audit.LoggerConfig, error) {
	if len(config) != 0 && string(config) != "{}" {
		grpcLogger.Warningf("Stdout logger doesn't support custom configs. Ignoring:\n%s", string(config))
	}
	return &loggerConfig{}, nil
}

func convertEvent(auditEvent *audit.Event) []any {
	return []any{
		"action", "audit",
		"rpc_method", auditEvent.FullMethodName,
		"principal", auditEvent.Principal,
		"policy_name", auditEvent.PolicyName,
		"matched_rule", auditEvent.MatchedRule,
		"authorized", auditEvent.Authorized,
		"timestamp", time.Now().Format(time.RFC3339Nano),
	}
}
