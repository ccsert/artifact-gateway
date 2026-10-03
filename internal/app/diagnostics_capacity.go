package app

import (
	"context"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/localcapacity"
)

func diagnosticLocalCapacity(ctx context.Context, observer *localcapacity.Observer) *adminopenapi.DiagnosticLocalCapacity {
	s := observer.Snapshot(ctx)
	mounts := make([]adminopenapi.DiagnosticLocalCapacityMount, len(s.Mounts))
	for i, m := range s.Mounts {
		shared := make([]adminopenapi.LocalCapacityAlias, len(m.SharedWith))
		for j, alias := range m.SharedWith {
			shared[j] = adminopenapi.LocalCapacityAlias(alias)
		}
		mounts[i] = adminopenapi.DiagnosticLocalCapacityMount{
			Alias: adminopenapi.LocalCapacityAlias(m.Alias), Status: adminopenapi.LocalCapacityStatus(m.Status),
			SampleAt: m.SampleAt, TotalBytes: m.TotalBytes, AvailableBytes: m.AvailableBytes, SharedWith: shared,
		}
		if m.Reason != "" {
			reason := adminopenapi.LocalCapacityReason(m.Reason)
			mounts[i].Reason = &reason
		}
	}
	return &adminopenapi.DiagnosticLocalCapacity{
		CheckedAt: s.CheckedAt, Source: "statfs", Scope: "observer_mount_namespace", Unit: "bytes",
		RefreshIntervalSeconds: s.RefreshIntervalSeconds, MaxSampleAgeSeconds: s.MaxSampleAgeSeconds,
		TimeoutMilliseconds: s.TimeoutMilliseconds, Mounts: mounts,
	}
}
