package repository

import "testing"

func TestSupportedFormatProfilesAreCompleteAndUnique(t *testing.T) {
	profiles := SupportedFormatProfiles()
	if len(profiles) != 8 {
		t.Fatalf("profiles=%d want=8", len(profiles))
	}
	seen := make(map[Format]bool, len(profiles))
	for _, profile := range profiles {
		if seen[profile.Format] {
			t.Fatalf("duplicate format profile %q", profile.Format)
		}
		seen[profile.Format] = true
		if !profile.AnonymousRead {
			t.Errorf("format %q group=%t anonymous=%t", profile.Format, profile.GroupSupported, profile.AnonymousRead)
		}
		if profile.Format == FormatAPT {
			if profile.PublicationScanning || FormatSupportsPublicationScanning(profile.Format, RepositoryTypeProxy) {
				t.Errorf("protocol-only format advertises publication scanning: %#v", profile)
			}
			if len(profile.RepositoryTypes) != 1 || profile.RepositoryTypes[0] != RepositoryTypeProxy || !profile.GroupSupported {
				t.Errorf("APT must expose only Proxy and Group: %#v", profile)
			}
			if len(profile.HostedOperations) != 0 || !FormatSupportsOperation(profile.Format, RepositoryTypeProxy, RepositoryOperationRead) || !FormatSupportsOperation(profile.Format, RepositoryTypeProxy, RepositoryOperationBrowse) {
				t.Errorf("APT advertises unsupported capabilities: %#v", profile)
			}
			continue
		}
		if profile.Format == FormatGo {
			if !profile.PublicationScanning || !FormatSupportsPublicationScanning(profile.Format, RepositoryTypeHosted) {
				t.Errorf("Go Hosted publication scanning is missing: %#v", profile)
			}
			if len(profile.RepositoryTypes) != 2 || !FormatSupportsRepositoryType(profile.Format, RepositoryTypeHosted) || !FormatSupportsRepositoryType(profile.Format, RepositoryTypeProxy) || !profile.GroupSupported {
				t.Errorf("Go must expose Hosted, Proxy, and Group: %#v", profile)
			}
			for _, operation := range []RepositoryOperation{RepositoryOperationRead, RepositoryOperationPublish, RepositoryOperationBrowse, RepositoryOperationDelete, RepositoryOperationRestore, RepositoryOperationRetain, RepositoryOperationReclaim} {
				if !FormatSupportsOperation(profile.Format, RepositoryTypeHosted, operation) {
					t.Errorf("Go Hosted missing operation %q", operation)
				}
			}
			for _, operation := range []RepositoryOperation{RepositoryOperationRead, RepositoryOperationBrowse} {
				if !FormatSupportsOperation(profile.Format, RepositoryTypeProxy, operation) {
					t.Errorf("Go Proxy missing operation %q", operation)
				}
			}
			for _, operation := range []RepositoryOperation{RepositoryOperationPublish, RepositoryOperationDelete, RepositoryOperationRestore, RepositoryOperationRetain, RepositoryOperationReclaim} {
				if FormatSupportsOperation(profile.Format, RepositoryTypeProxy, operation) {
					t.Errorf("Go Proxy advertises unsupported operation %q", operation)
				}
			}
			continue
		}
		if !FormatSupportsRepositoryType(profile.Format, RepositoryTypeHosted) {
			t.Errorf("format %q repository types=%v", profile.Format, profile.RepositoryTypes)
		}
		if !profile.PublicationScanning || !FormatSupportsPublicationScanning(profile.Format, RepositoryTypeHosted) || FormatSupportsPublicationScanning(profile.Format, RepositoryTypeProxy) {
			t.Errorf("format %q publication scanning capability is inconsistent: %#v", profile.Format, profile)
		}
		if profile.Format == FormatNPM {
			if !profile.GroupSupported || !FormatSupportsRepositoryType(profile.Format, RepositoryTypeProxy) {
				t.Errorf("npm must support Hosted, Proxy, and Group: %#v", profile)
			}
			for _, operation := range []RepositoryOperation{RepositoryOperationRead, RepositoryOperationPublish, RepositoryOperationBrowse, RepositoryOperationDelete, RepositoryOperationRestore, RepositoryOperationRetain, RepositoryOperationReclaim, RepositoryOperationPromote, RepositoryOperationReplicate} {
				if !FormatSupportsOperation(profile.Format, RepositoryTypeHosted, operation) {
					t.Errorf("npm missing hosted operation %q", operation)
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
	for _, format := range []Format{FormatOCI, FormatMaven, FormatConan, FormatRaw, FormatNPM, FormatPyPI, FormatGo, FormatAPT} {
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
	if IsSupportedFormat("rubygems") || FormatSupportsRepositoryType("rubygems", RepositoryTypeHosted) || FormatSupportsOperation("rubygems", RepositoryTypeHosted, RepositoryOperationRead) || FormatSupportsPublicationScanning("rubygems", RepositoryTypeHosted) {
		t.Fatal("unknown format was admitted")
	}
}

func TestAPTHostedProvisioningDoesNotAdvertiseProtocolCapabilities(t *testing.T) {
	profile, ok := FormatProfileFor(FormatAPT)
	if !ok || FormatSupportsRepositoryType(FormatAPT, RepositoryTypeHosted) ||
		!FormatSupportsRepositoryProvisioning(FormatAPT, RepositoryTypeHosted) {
		t.Fatalf("APT profile=%#v found=%t", profile, ok)
	}
	if len(profile.RepositoryTypes) != 1 || profile.RepositoryTypes[0] != RepositoryTypeProxy || len(profile.HostedOperations) != 0 {
		t.Fatalf("APT Hosted leaked into advertised capabilities: %#v", profile)
	}
}

func TestWorkerFormatsReflectExecutableBackgroundWork(t *testing.T) {
	found := map[Format]bool{}
	for _, format := range WorkerFormats() {
		found[format] = true
	}
	for _, format := range []Format{FormatNPM, FormatPyPI, FormatGo, FormatAPT} {
		if !found[format] {
			t.Fatalf("%s lifecycle workers are missing", format)
		}
	}
	profile, ok := FormatProfileFor(FormatGo)
	if !ok || !FormatSupportsOperation(FormatGo, RepositoryTypeHosted, RepositoryOperationRetain) || !FormatSupportsOperation(FormatGo, RepositoryTypeHosted, RepositoryOperationReclaim) {
		t.Fatalf("Go Hosted retention capabilities are missing: %#v", profile)
	}
}
