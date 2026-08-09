package repository

import "testing"

func TestSupportedFormatProfilesAreCompleteAndUnique(t *testing.T) {
	profiles := SupportedFormatProfiles()
	if len(profiles) != 5 {
		t.Fatalf("profiles=%d want=5", len(profiles))
	}
	seen := make(map[Format]bool, len(profiles))
	for _, profile := range profiles {
		if seen[profile.Format] {
			t.Fatalf("duplicate format profile %q", profile.Format)
		}
		seen[profile.Format] = true
		if !FormatSupportsRepositoryType(profile.Format, RepositoryTypeHosted) {
			t.Errorf("format %q repository types=%v", profile.Format, profile.RepositoryTypes)
		}
		if !profile.AnonymousRead {
			t.Errorf("format %q group=%t anonymous=%t", profile.Format, profile.GroupSupported, profile.AnonymousRead)
		}
		if profile.Format == FormatNPM {
			if profile.GroupSupported || !FormatSupportsRepositoryType(profile.Format, RepositoryTypeProxy) {
				t.Errorf("npm must support Hosted and Proxy without Group: %#v", profile)
			}
			for _, operation := range []RepositoryOperation{RepositoryOperationRead, RepositoryOperationPublish, RepositoryOperationBrowse} {
				if !FormatSupportsOperation(profile.Format, RepositoryTypeHosted, operation) {
					t.Errorf("npm missing hosted operation %q", operation)
				}
			}
			for _, operation := range []RepositoryOperation{RepositoryOperationDelete, RepositoryOperationRestore, RepositoryOperationRetain, RepositoryOperationReclaim, RepositoryOperationPromote, RepositoryOperationReplicate} {
				if FormatSupportsOperation(profile.Format, RepositoryTypeHosted, operation) {
					t.Errorf("npm advertises unimplemented operation %q", operation)
				}
			}
			for _, operation := range []RepositoryOperation{RepositoryOperationRead, RepositoryOperationBrowse} {
				if !FormatSupportsOperation(profile.Format, RepositoryTypeProxy, operation) {
					t.Errorf("npm missing proxy operation %q", operation)
				}
			}
			for _, operation := range []RepositoryOperation{RepositoryOperationPublish, RepositoryOperationDelete, RepositoryOperationRestore, RepositoryOperationRetain, RepositoryOperationReclaim, RepositoryOperationPromote, RepositoryOperationReplicate} {
				if FormatSupportsOperation(profile.Format, RepositoryTypeProxy, operation) {
					t.Errorf("npm proxy advertises unimplemented operation %q", operation)
				}
			}
			continue
		}
		if len(profile.RepositoryTypes) != 2 || !FormatSupportsRepositoryType(profile.Format, RepositoryTypeProxy) || !profile.GroupSupported {
			t.Errorf("format %q repository types=%v group=%t", profile.Format, profile.RepositoryTypes, profile.GroupSupported)
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
	for _, format := range []Format{FormatOCI, FormatMaven, FormatConan, FormatRaw, FormatNPM} {
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
	if IsSupportedFormat("pypi") || FormatSupportsRepositoryType("pypi", RepositoryTypeHosted) || FormatSupportsOperation("pypi", RepositoryTypeHosted, RepositoryOperationRead) {
		t.Fatal("unknown format was admitted")
	}
}

func TestWorkerFormatsExcludeProtocolOnlyFormats(t *testing.T) {
	for _, format := range WorkerFormats() {
		if format == FormatNPM {
			t.Fatal("npm has no background lifecycle capability yet")
		}
	}
}
