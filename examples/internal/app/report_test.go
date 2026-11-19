package app_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ti/common-go/graceful"
)

type restReport struct {
	ReportID      string `json:"report_id"`
	State         string `json:"state"`
	DoneSections  int    `json:"done_sections"`
	TotalSections int    `json:"total_sections"`
	Error         string `json:"error"`
	Summary       string `json:"summary"`
}

func (e *env) startReport(t *testing.T, body string) restReport {
	t.Helper()
	code, out := e.rest(t, http.MethodPost, "/v1/reports", body)
	if code != http.StatusOK {
		t.Fatalf("StartReport: %d %s", code, out)
	}
	var resp struct{ Report restReport }
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("StartReport body %q: %v", out, err)
	}
	return resp.Report
}

// watch reads the WatchReport stream (newline-delimited JSON over REST) to
// its end and returns the reports and the error line, if any.
func (e *env) watch(t *testing.T, id string) (reports []restReport, streamErr string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, e.httpBase+"/v1/reports/"+id+":watch", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("WatchReport: status %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var line struct {
			Result struct{ Report restReport } `json:"result"`
			Error  json.RawMessage             `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		if line.Error != nil {
			return reports, string(line.Error)
		}
		reports = append(reports, line.Result.Report)
	}
	return reports, ""
}

// A unary method starts a job, a server-streaming method watches it to the
// end; the same request_id returns the same job.
func TestReportStartAndWatch(t *testing.T) {
	e := start(t)
	report := e.startReport(t, `{"sections":5,"section_millis":20,"request_id":"req-1"}`)
	if report.ReportID == "" || (report.State != "STATE_QUEUED" && report.State != "STATE_RUNNING") {
		t.Fatalf("started report = %+v", report)
	}
	if again := e.startReport(t, `{"sections":5,"section_millis":20,"request_id":"req-1"}`); again.ReportID != report.ReportID {
		t.Fatalf("same request_id started another report: %s vs %s", again.ReportID, report.ReportID)
	}

	reports, streamErr := e.watch(t, report.ReportID)
	if streamErr != "" || len(reports) == 0 {
		t.Fatalf("watch: %d reports, error %s", len(reports), streamErr)
	}
	final := reports[len(reports)-1]
	if final.State != "STATE_SUCCEEDED" || final.DoneSections != 5 || final.TotalSections != 5 ||
		final.Summary != "report with 5 sections" {
		t.Fatalf("final = %+v", final)
	}
	for i := 1; i < len(reports); i++ {
		if reports[i].DoneSections < reports[i-1].DoneSections {
			t.Fatalf("progress went backwards: %+v", reports)
		}
	}
	// Still retrievable after it is done.
	code, body := e.rest(t, http.MethodGet, "/v1/reports/"+report.ReportID, "")
	if code != http.StatusOK || !strings.Contains(body, "STATE_SUCCEEDED") {
		t.Fatalf("GetReport: %d %s", code, body)
	}
	if code, body = e.rest(t, http.MethodGet, "/v1/reports/unknown", ""); code != http.StatusNotFound {
		t.Fatalf("GetReport(unknown): %d %s", code, body)
	}
	if code, body = e.rest(t, http.MethodPost, "/v1/reports", `{"sections":0}`); code != http.StatusBadRequest {
		t.Fatalf("StartReport(invalid): %d %s", code, body)
	}
}

// CancelReport stops a running job; the watcher sees CANCELLED.
func TestReportCancel(t *testing.T) {
	e := start(t)
	report := e.startReport(t, `{"sections":1000,"section_millis":50}`)
	done := make(chan []restReport, 1)
	go func() {
		reports, _ := e.watch(t, report.ReportID)
		done <- reports
	}()
	time.Sleep(100 * time.Millisecond)
	if code, body := e.rest(t, http.MethodPost, "/v1/reports/"+report.ReportID+":cancel", "{}"); code != http.StatusOK {
		t.Fatalf("CancelReport: %d %s", code, body)
	}
	select {
	case reports := <-done:
		if final := reports[len(reports)-1]; final.State != "STATE_CANCELLED" || final.DoneSections >= 1000 {
			t.Fatalf("final = %+v", final)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watch stream did not end after CancelReport")
	}
}

// At shutdown a watch stream ends with UNAVAILABLE as soon as the server
// drains (it does not hold the shutdown), and the server exits after the
// queue interrupted the job.
func TestReportShutdown(t *testing.T) {
	e := start(t)
	report := e.startReport(t, `{"sections":1000,"section_millis":60000}`) // would take hours
	type result struct {
		reports []restReport
		err     string
	}
	done := make(chan result, 1)
	go func() {
		reports, streamErr := e.watch(t, report.ReportID)
		done <- result{reports, streamErr}
	}()
	time.Sleep(200 * time.Millisecond) // the stream is open and the job is running

	begin := time.Now()
	graceful.Close() // what SIGTERM does; the test cleanup waits for Start to return
	select {
	case r := <-done:
		if len(r.reports) == 0 || r.reports[len(r.reports)-1].State != "STATE_RUNNING" {
			t.Fatalf("reports before the stream ended: %+v", r.reports)
		}
		if !strings.Contains(r.err, "shutting down") {
			t.Fatalf("stream ended with %q, want the UNAVAILABLE \"shutting down\" error", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watch stream held the shutdown")
	}
	if took := time.Since(begin); took > 3*time.Second {
		t.Fatalf("the watch stream ended %s after shutdown began", took)
	}
}
