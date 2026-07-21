package app

import "context"

func (h OCIHandler) anonymousOCIAllowed(ctx context.Context, groupName string) bool {
	if h.Resolver.Store == nil {
		return false
	}
	group, err := h.Resolver.Store.GetGroup(ctx, groupName)
	if err != nil || !group.Enabled || !group.Anonymous {
		return false
	}
	for _, member := range group.Members {
		if member.Anonymous {
			return true
		}
	}
	return false
}

func (h MavenHandler) anonymousMavenAllowed(ctx context.Context, groupName string) bool {
	if h.Store == nil {
		return false
	}
	group, err := h.Store.GetMavenGroup(ctx, groupName)
	if err != nil || !group.Enabled || !group.Anonymous {
		return false
	}
	for _, member := range group.Members {
		if member.Anonymous {
			return true
		}
	}
	return false
}

func (h RawHandler) anonymousRawAllowed(ctx context.Context, groupName string) bool {
	if h.Store == nil {
		return false
	}
	group, err := h.Store.GetGroup(ctx, groupName)
	if err != nil || !group.Enabled || !group.Anonymous {
		return false
	}
	for _, member := range group.Members {
		if member.Anonymous {
			return true
		}
	}
	return false
}
