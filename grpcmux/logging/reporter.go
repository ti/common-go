package logging

import (
	"context"
	"errors"
	"time"

	"github.com/ti/common-go/log"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type reporter struct {
	interceptors.CallMeta
	ctx      context.Context
	fields   logging.Fields
	logger   logging.Logger
	decision *Decision
	opts     *options
}

const (
	keyRequest  = "request"
	keyResponse = "response"
	keyDuration = "duration"
)

// PostCall the implement for post call, the post call
func (c *reporter) PostCall(err error, duration time.Duration) {
	fields := c.fields
	fields = append(fields, keyDuration, float32(duration.Nanoseconds()/1000)/1000)
	errStatus := statusFromError(err)
	if errStatus.Code() > 0 {
		fields = append(fields,
			"code", int(errStatus.Code()),
			"error", errStatus.Message(),
		)
	}
	c.logger.Log(c.ctx, codeToLevel(errStatus.Code()), "finished call", fields...)
}

// PostMsgSend the implement for send
func (c *reporter) PostMsgSend(payload any, err error, duration time.Duration) {
	logLvl := codeToLevel(status.Code(err))
	if c.CallMeta.IsClient {
		if c.decision.Start {
			fields := c.fields
			fields = append(fields, "duration", float32(duration.Nanoseconds()/1000)/1000)
			c.logger.Log(c.ctx, logLvl, "started call", fields...)
		}
		if c.decision.Request {
			c.fields = append(c.fields, keyRequest, c.opts.bodyEncoder.Encode(payload, c.decision.ClearData))
		}
	} else if c.decision.Response && err == nil {
		c.fields = append(c.fields, keyResponse, c.opts.bodyEncoder.Encode(payload, c.decision.ClearData))
	}
}

// PostMsgReceive the implement for receive
func (c *reporter) PostMsgReceive(payload any, err error, duration time.Duration) {
	logLvl := codeToLevel(status.Code(err))
	if !c.CallMeta.IsClient {
		if c.decision.Request {
			c.fields = append(c.fields, keyRequest, c.opts.bodyEncoder.Encode(payload, c.decision.ClearData))
		}
		if c.decision.Start {
			fields := c.fields
			fields = append(fields, keyDuration, float32(duration.Nanoseconds()/1000)/1000)
			c.logger.Log(c.ctx, logLvl, "started call", fields...)
		}
	} else if c.decision.Response && err == nil {
		c.fields = append(c.fields, keyResponse, c.opts.bodyEncoder.Encode(payload, c.decision.ClearData))
	}
}

func reportable(logger logging.Logger, opts *options) interceptors.CommonReportableFunc {
	return func(ctx context.Context, c interceptors.CallMeta) (interceptors.Reporter, context.Context) {
		decision := opts.shouldLog(ctx, c)
		if !decision.Enable {
			return &ignoreReporter{}, ctx
		}
		ctx = log.NewOrFromContext(ctx, log.Default(false))
		kind := logging.KindServerFieldValue
		if c.IsClient {
			kind = logging.KindClientFieldValue
		}
		fields := logging.ExtractFields(ctx)
		fields = append(fields, logging.Fields{
			"action", "/" + c.Service + "/" + c.Method,
			"protocol", "grpc/" + kind + "/" + string(c.Typ),
		}...)

		if !c.IsClient {
			if remotePeer, ok := peer.FromContext(ctx); ok {
				fields = append(fields, "peer", remotePeer.Addr.String())
			}
		}
		if d, ok := ctx.Deadline(); ok {
			fields = append(fields, "deadline", d.Format(time.RFC3339))
		}
		return &reporter{
			CallMeta: c,
			ctx:      ctx,
			fields:   fields,
			logger:   logger,
			decision: decision,
			opts:     opts,
		}, logging.InjectFields(ctx, fields)
	}
}

// codeToLevel the default grpc status code to log level
func codeToLevel(code codes.Code) logging.Level {
	if code < 100 {
		switch code {
		case codes.OK, codes.Canceled, codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.ResourceExhausted,
			codes.FailedPrecondition, codes.Aborted, codes.OutOfRange, codes.PermissionDenied, codes.Unauthenticated:
			return logging.LevelInfo
		case codes.DeadlineExceeded, codes.Unavailable, codes.DataLoss, codes.Unimplemented:
			return logging.LevelWarn
		case codes.Unknown, codes.Internal:
			return logging.LevelError
		default:
			return logging.LevelWarn
		}
	}
	for code >= 10 {
		code /= 10
	}
	switch code {
	case statusOKPrefix:
		return logging.LevelInfo
	case statusBadRequestPrefix:
		return logging.LevelWarn
	default:
		return logging.LevelWarn
	}
}

func statusFromError(err error) *status.Status {
	if err == nil {
		return status.New(codes.OK, "")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.New(codes.DeadlineExceeded, err.Error())
	}
	if errors.Is(err, context.Canceled) {
		return status.New(codes.Canceled, err.Error())
	}
	return status.Convert(err)
}

const (
	statusOKPrefix         = 2
	statusBadRequestPrefix = 4
)

type ignoreReporter struct{}

func (i *ignoreReporter) PostCall(error, time.Duration) {}

func (i *ignoreReporter) PostMsgSend(any, error, time.Duration) {}

func (i *ignoreReporter) PostMsgReceive(any, error, time.Duration) {}
