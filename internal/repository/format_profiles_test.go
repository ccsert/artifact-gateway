package repository

import "testing"

func TestSupportedFormatProfilesAreCompleteAndUnique(t *testing.T) {
	profiles := SupportedFormatProfiles()
	if len(profiles) != 4 {
		t.Fatalf("profiles=%d want=4", len(profiles))
	}
	seen := make(map[Format]bool, len(profiles))
	for _, profile := range profiles {
		if seen[profile.Format] {
			t.Fatalf("duplicate format profile %q", profile.Format)
		}
		seen[profile.Format] = true
		if len(profile.RepositoryTypes) != 2 || !FormatSupportsRepositoryType(profile.Format, RepositoryTypeHosted) || !FormatSupportsRepositoryType(profile.Format, RepositoryTypeProxy) {
			t.Errorf("format %q repository types=%v", profile.Format, profile.RepositoryTypes)
		}
		if !profile.GroupSupported || !profile.AnonymousRead {
			t.Errorf("format %q group=%t anonymous=%t", profile.Format, profile.GroupSupported, profile.AnonymousRead)
		}
		for _, operation := range []RepositoryOperation{
			RepositoryOperationRead,
			RepositoryOperationPublish,
			RepositoryOperationBrowse,
			RepositoryOperationDelete,
			RepositoryOperationRestore,
			RepositoryOperationRetain,
			RepositoryOperationReclaim,
			RepositoryOperationPromote,
			RepositoryOperationReplicate,
		} {
			if !FormatSupportsOperation(profile.Format, RepositoryTypeHosted, operation) {
				t.Errorf("format %q missing hosted operation %q", profile.Format, operation)
			}
		}
		for _, operation := range []RepositoryOperation{RepositoryOperationRead, RepositoryOperationBrowse, RepositoryOperationReclaim} {
			if !FormatSupportsOperation(profile.Format, RepositoryTypeProxy, operation) {
				t.Errorf("format %q missing proxy operation %q", profile.Format, operation)
			}
		}
	}
	for _, format := range []Format{FormatOCI, FormatMaven, FormatConan, FormatRaw} {
		if !seen[format] || !IsSupportedFormat(format) {
			t.Errorf("format %q is missing", format)
		}
	}
}

func TestSupportedFormatProfilesReturnDefensiveCopies(t *testing.T) {
	profiles := SupportedFormatProfiles()
	profiles[0].RepositoryTypes[0] = "changed"
	profiles[0].HostedOperations[0] = "changed"
	profiles[0].ProxyOperations[0] = "changed"

	profile, ok := FormatProfileFor(profiles[0].Format)
	if !ok {
		t.Fatal("profile disappeared")
	}
	if profile.RepositoryTypes[0] != RepositoryTypeHosted || profile.HostedOperations[0] != RepositoryOperationRead || profile.ProxyOperations[0] != RepositoryOperationRead {
		t.Fatalf("profile mutation leaked: %#v", profile)
	}
}

func TestUnknownFormatHasNoCapabilities(t *testing.T) {
	if IsSupportedFormat("npm") || FormatSupportsRepositoryType("npm", RepositoryTypeHosted) || FormatSupportsOperation("npm", RepositoryTypeHosted, RepositoryOperationRead) {
		t.Fatal("unknown format was admitted")
	}
}
