package app

import (
	"context"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/httperrorrate"
)

// Fixed business classes reuse the HTTP instrumentation's bounded counters.
// Health and metrics probes and the other status bucket are excluded.
var errorRateClasses = [httperrorrate.ClassCount]requestClass{
	requestClassManagement, requestClassOCI, requestClassMaven, requestClassRaw,
	requestClassConan, requestClassNPM, requestClassPyPI, requestClassGo,
	requestClassCargo, requestClassOther,
}

func (m *Metrics) sampleHTTPErrorRate(o *httperrorrate.Observer, sessionID string, at time.Time) {
	var counters httperrorrate.Counters
	for i, class := range errorRateClasses {
		for status := range counters[i] {
			counters[i][status] = m.httpRequests.requests[class][status].Load()
		}
	}
	o.Record(at, sessionID, counters)
}

// StartHTTPErrorRateSampling is called once by the API runtime, using the same
// Metrics that instruments its server. Diagnostics reads snapshots only.
func (m *Metrics) StartHTTPErrorRateSampling(ctx context.Context, instanceID, sessionID string) (*httperrorrate.Observer, <-chan struct{}) {
	o := httperrorrate.New(instanceID, sessionID)
	done := make(chan struct{})
	m.sampleHTTPErrorRate(o, sessionID, time.Now().UTC())
	go func() {
		defer close(done)
		ticker := time.NewTicker(httperrorrate.SampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.sampleHTTPErrorRate(o, sessionID, time.Now().UTC())
			}
		}
	}()
	return o, done
}

func diagnosticHTTPErrorRate(o *httperrorrate.Observer, runtime DiagnosticRuntime, now time.Time) *adminopenapi.DiagnosticHTTPErrorRate {
	s := httperrorrate.Snapshot{InstanceID: runtime.InstanceID, SessionID: runtime.SessionID, CheckedAt: now, State: "unknown", Reason: "source_unavailable"}
	if o != nil {
		observed := o.Snapshot(now)
		// A misbound observer must never be attributed to the responding runtime.
		if observed.InstanceID == runtime.InstanceID && observed.SessionID == runtime.SessionID {
			s = observed
		}
	}
	r := &adminopenapi.DiagnosticHTTPErrorRate{
		CheckedAt: s.CheckedAt, InstanceId: s.InstanceID, SessionId: s.SessionID,
		Source: "artifact_gateway_http_requests_total", Scope: "responding_process_business_http",
		State: adminopenapi.HTTPErrorRateState(s.State), SampleAt: s.SampleAt,
		WindowSeconds: int(httperrorrate.Window / time.Second), MinimumRequests: int(httperrorrate.MinimumRequests),
		SampleIntervalSeconds: int(httperrorrate.SampleInterval / time.Second), MaxSampleAgeSeconds: int(httperrorrate.MaxSampleAge / time.Second),
		WindowStart: s.WindowStart, WindowEnd: s.WindowEnd, CoverageSeconds: s.CoverageSeconds, Ratio: s.Ratio,
	}
	if s.Reason != "" {
		reason := adminopenapi.HTTPErrorRateReason(s.Reason)
		r.Reason = &reason
	}
	if s.Requests != nil {
		n := int64(*s.Requests)
		r.Requests = &n
	}
	if s.Errors != nil {
		n := int64(*s.Errors)
		r.Errors = &n
	}
	return r
}
