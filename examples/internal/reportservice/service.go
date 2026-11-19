// Package reportservice is the example of background work with observable
// status: StartReport submits a job to a background.Queue and returns at
// once, WatchReport relays the job's state and progress as a server stream,
// CancelReport cancels it. See docs/guides/background-work.md.
package reportservice

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ti/common-go/background"
	reportv1 "github.com/ti/common-go/examples/gen/go/example/report/v1"
)

// kind is the job kind of a report in the queue.
const kind = "report"

// Service implements reportv1.ReportServiceServer on top of a job queue.
type Service struct {
	reportv1.UnimplementedReportServiceServer
	queue *background.Queue
}

// New creates the service and its queue. The queue registers its shutdown
// with package graceful, which closes it after the server has stopped
// serving and before the dependencies.
//
// Jobs live in process memory (the default store): they are lost when the
// process restarts and are invisible to other replicas. Pass a durable
// background.Store in QueueOptions to change that.
func New(opts background.QueueOptions) *Service {
	if opts.Name == "" {
		opts.Name = "reports"
	}
	s := &Service{queue: background.NewQueue(opts)}
	s.queue.Handle(kind, s.build, background.HandlerOptions{
		Timeout:     10 * time.Minute, // one attempt
		MaxAttempts: 3,                // for errors marked background.Retryable
	})
	return s
}

// build is the job handler: it runs in the background, detached from the
// request that started it.
func (s *Service) build(ctx context.Context, run *background.Run) ([]byte, error) {
	var req reportv1.StartReportRequest
	if err := proto.Unmarshal(run.Job().Payload, &req); err != nil {
		return nil, err // not retryable: the payload will not get better
	}
	total := int64(req.GetSections())
	run.SetProgress(0, total, "")
	for done := range total {
		// Honor cancellation (CancelReport, timeout, shutdown) between
		// units of work; a real handler passes ctx to its I/O calls.
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-time.After(time.Duration(req.GetSectionMillis()) * time.Millisecond):
		}
		run.SetProgress(done+1, total, "")
	}
	return fmt.Appendf(nil, "report with %d sections", total), nil
}

// StartReport implements ReportService.StartReport. Errors from Submit carry
// their gRPC code (ErrQueueFull is RESOURCE_EXHAUSTED, ErrShuttingDown is
// UNAVAILABLE), so they are returned as they are.
func (s *Service) StartReport(ctx context.Context, req *reportv1.StartReportRequest) (*reportv1.StartReportResponse, error) {
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	job, _, err := s.queue.Submit(ctx, background.SubmitRequest{
		Kind:    kind,
		Key:     req.GetRequestId(), // idempotent submission
		Payload: payload,
	})
	if err != nil {
		return nil, err
	}
	return &reportv1.StartReportResponse{Report: toProto(job)}, nil
}

// GetReport implements ReportService.GetReport.
func (s *Service) GetReport(ctx context.Context, req *reportv1.GetReportRequest) (*reportv1.GetReportResponse, error) {
	job, err := s.queue.Get(ctx, req.GetReportId())
	if err != nil {
		return nil, err // ErrNotFound is NOT_FOUND
	}
	return &reportv1.GetReportResponse{Report: toProto(job)}, nil
}

// WatchReport implements ReportService.WatchReport: one message per change,
// the last one with a state that is done. The watch ends with UNAVAILABLE
// (ErrShuttingDown) as soon as the server starts draining, so the stream
// never holds the shutdown.
func (s *Service) WatchReport(req *reportv1.WatchReportRequest, stream grpc.ServerStreamingServer[reportv1.WatchReportResponse]) error {
	for job, err := range s.queue.Watch(stream.Context(), req.GetReportId()) {
		if err != nil {
			return err
		}
		if err := stream.Send(&reportv1.WatchReportResponse{Report: toProto(job)}); err != nil {
			return err
		}
	}
	return nil
}

// CancelReport implements ReportService.CancelReport.
func (s *Service) CancelReport(ctx context.Context, req *reportv1.CancelReportRequest) (*reportv1.CancelReportResponse, error) {
	job, err := s.queue.Cancel(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	return &reportv1.CancelReportResponse{Report: toProto(job)}, nil
}

var states = map[background.State]reportv1.State{
	background.StateQueued:      reportv1.State_STATE_QUEUED,
	background.StateRunning:     reportv1.State_STATE_RUNNING,
	background.StateSucceeded:   reportv1.State_STATE_SUCCEEDED,
	background.StateFailed:      reportv1.State_STATE_FAILED,
	background.StateCancelled:   reportv1.State_STATE_CANCELLED,
	background.StateInterrupted: reportv1.State_STATE_INTERRUPTED,
}

func toProto(job background.Job) *reportv1.Report {
	report := &reportv1.Report{
		ReportId:      job.ID,
		State:         states[job.State],
		DoneSections:  int32(job.Progress.Done),  //nolint:gosec // bounded by request validation
		TotalSections: int32(job.Progress.Total), //nolint:gosec // bounded by request validation
		Summary:       string(job.Result),
		CreateTime:    timestamppb.New(job.CreatedAt),
	}
	if job.State.Done() {
		report.Error = job.Error
		report.FinishTime = timestamppb.New(job.FinishedAt)
	}
	return report
}
