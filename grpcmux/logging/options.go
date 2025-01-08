package logging

import (
	"context"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	pref "google.golang.org/protobuf/reflect/protoreflect"
)

type options struct {
	shouldLog   Decider
	bodyEncoder *bodyEncoder
}

// Option the Options for this module.
type Option func(*options)

// WithDecider customizes the function for deciding if the gRPC interceptor logs should log.
func WithDecider(f Decider) Option {
	return func(o *options) {
		o.shouldLog = f
	}
}

// WithBodyMaskField mask field for logging
func WithBodyMaskField(fields ...string) Option {
	return func(o *options) {
		for _, f := range fields {
			o.bodyEncoder.maskFields[pref.Name(f)] = true
		}
	}
}

// DefaultLoggingDecider is the default implementation of decider to see if you should log the call
// by default this if always true so all calls are logged.
func DefaultLoggingDecider(_ context.Context, callMeta interceptors.CallMeta) *Decision {
	if callMeta.Service == healthpb.Health_ServiceDesc.ServiceName {
		return &Decision{}
	}
	return &Decision{
		Enable:   true,
		Request:  false,
		Response: false,
	}
}

// DefaultLoggingBodyDecider is the default implementation of decider to see if you should log body the call
// by default this if always true so all calls are logged.
func DefaultLoggingBodyDecider(_ context.Context, callMeta interceptors.CallMeta) *Decision {
	if callMeta.Service == healthpb.Health_ServiceDesc.ServiceName {
		return &Decision{}
	}
	dec := &Decision{
		Enable: true,
	}
	if callMeta.Typ == interceptors.ClientStream || callMeta.Typ == interceptors.ServerStream {
		dec.Start = true
	}
	dec.Response = true
	dec.Request = true
	dec.ClearData = false
	return dec
}

// Decider function defines rules for suppressing any interceptor logs.
type Decider func(context.Context, interceptors.CallMeta) *Decision

// Decision defines rules for enabling Request or Response logging.
type Decision struct {
	Enable    bool
	Start     bool
	Request   bool
	Response  bool
	ClearData bool
}
