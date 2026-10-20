package nexussource

import (
	"context"
	"errors"
	"slices"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

// PreviewMapping re-reads only source metadata. It never creates target repositories.
func (s *Service) PreviewMapping(ctx context.Context, id string, mappings []Mapping) (MappingPreview, error) {
	if len(mappings) < 1 || len(mappings) > 10 {
		return MappingPreview{}, fail("invalid_request")
	}
	names := make([]string, 0, len(mappings))
	bySource := map[string]Mapping{}
	targetCounts := map[string]int{}
	for _, m := range mappings {
		if !sourceName.MatchString(m.SourceRepository) {
			return MappingPreview{}, fail("invalid_request")
		}
		if _, ok := bySource[m.SourceRepository]; ok {
			return MappingPreview{}, fail("invalid_request")
		}
		names = append(names, m.SourceRepository)
		bySource[m.SourceRepository] = m
		targetCounts[m.TargetName]++
	}
	remote, source, done, err := s.connect(ctx, id)
	if err != nil {
		return MappingPreview{}, err
	}
	defer done()
	all, err := remote.repositories()
	if err != nil {
		return MappingPreview{}, err
	}
	selected, err := selectRepositories(all, names)
	if err != nil {
		return MappingPreview{}, err
	}
	inspected := map[string]Repository{}
	for _, r := range selected {
		r, err = remote.inspect(r)
		if err != nil {
			return MappingPreview{}, err
		}
		inspected[r.Name] = r
	}
	out := MappingPreview{SourceID: source.ID, SourceVersion: source.Version, Valid: true, Mappings: []MappingResult{}}
	for _, m := range mappings {
		r := inspected[m.SourceRepository]
		result := MappingResult{SourceRepository: m.SourceRepository, TargetName: m.TargetName, Status: "ready", Reasons: []string{}, TargetMembers: []string{}, Fields: r.Fields}
		reject := func(status, reason string) {
			if result.Status != "invalid" {
				result.Status = status
			}
			result.Reasons = append(result.Reasons, reason)
		}
		if !targetName.MatchString(m.TargetName) || targetCounts[m.TargetName] > 1 {
			reject("invalid", "target_name_invalid_or_duplicate")
		}
		format, supported := NativeFormat(r.Format)
		if !supported || m.Format != format {
			reject("invalid", "target_format_mismatch")
		}
		if (m.Type != "hosted" && m.Type != "proxy" && m.Type != "group") || m.Type != r.Type {
			reject("invalid", "target_type_mismatch")
		}
		if r.Status == "unsupported" {
			reject("unsupported", r.Reason)
		}
		if s.targets == nil {
			return MappingPreview{}, fail("target_lookup_unavailable")
		}
		target, lookupErr := s.targets.GetHostedRepositoryByName(remote.ctx, m.TargetName)
		if lookupErr == nil {
			if string(target.Format) != m.Format || string(target.Type) != m.Type || string(target.State) != "active" {
				reject("invalid", "existing_target_conflict")
			}
		} else if !errors.Is(lookupErr, repository.ErrNotFound) {
			return MappingPreview{}, fail("target_lookup_unavailable")
		}
		group, groupErr := s.targets.GetHostedGroupByName(remote.ctx, m.TargetName)
		if groupErr == nil {
			if m.Type != "group" || string(group.Format) != m.Format {
				reject("invalid", "existing_target_conflict")
			} else if result.Status == "ready" {
				result.Status = "manual"
				result.Reasons = append(result.Reasons, "existing_group_requires_explicit_configuration")
			}
		} else if !errors.Is(groupErr, repository.ErrNotFound) {
			return MappingPreview{}, fail("target_lookup_unavailable")
		}

		if r.Type == "group" {
			for _, member := range r.Members {
				mapped, ok := bySource[member]
				if !ok {
					reject("invalid", "group_member_mapping_missing")
					continue
				}
				sourceMember := inspected[member]
				if mapped.Format != m.Format || sourceMember.Type == "group" || sourceMember.Status == "unsupported" {
					reject("invalid", "group_member_mapping_invalid")
				}
				result.TargetMembers = append(result.TargetMembers, mapped.TargetName)
			}
			if !slices.Equal(result.TargetMembers, m.TargetMembers) {
				reject("invalid", "group_member_order_mismatch")
			}
		} else if len(m.TargetMembers) > 0 {
			reject("invalid", "members_require_group")
		}
		if r.Status == "preview_only" && result.Status == "ready" {
			result.Status = "manual"
			result.Reasons = append(result.Reasons, "configure_native_policies_and_credentials_explicitly")
		}
		if result.Status == "invalid" || result.Status == "unsupported" {
			out.Valid = false
		}
		out.Mappings = append(out.Mappings, result)
	}
	// A Group cannot be ready when any explicitly mapped dependency is invalid.
	statuses := map[string]string{}
	for _, r := range out.Mappings {
		statuses[r.SourceRepository] = r.Status
	}
	for i, m := range mappings {
		if m.Type != "group" {
			continue
		}
		for _, member := range inspected[m.SourceRepository].Members {
			if statuses[member] == "invalid" || statuses[member] == "unsupported" {
				out.Mappings[i].Status = "invalid"
				out.Mappings[i].Reasons = append(out.Mappings[i].Reasons, "group_member_mapping_invalid")
				out.Valid = false
				break
			}
		}
	}
	out.Stats = remote.stats
	return out, nil
}
