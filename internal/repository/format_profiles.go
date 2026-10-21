package repository

// RepositoryOperation is a format capability exposed by a repository type.
type RepositoryOperation string

const (
	RepositoryOperationRead      RepositoryOperation = "read"
	RepositoryOperationPublish   RepositoryOperation = "publish"
	RepositoryOperationBrowse    RepositoryOperation = "browse"
	RepositoryOperationDelete    RepositoryOperation = "delete"
	RepositoryOperationRestore   RepositoryOperation = "restore"
	RepositoryOperationRetain    RepositoryOperation = "retain"
	RepositoryOperationReclaim   RepositoryOperation = "reclaim"
	RepositoryOperationPromote   RepositoryOperation = "promote"
	RepositoryOperationReplicate RepositoryOperation = "replicate"
	RepositoryOperationYank      RepositoryOperation = "yank"
	RepositoryOperationDeprecate RepositoryOperation = "deprecate"
)

// FormatProfile is the capability contract for one supported artifact format.
// Protocol routing and persistence remain format-owned; this profile controls
// admission and capability discovery across management surfaces.
type FormatProfile struct {
	Format              Format
	RepositoryTypes     []RepositoryType
	GroupSupported      bool
	AnonymousRead       bool
	PublicationScanning bool
	HostedOperations    []RepositoryOperation
	ProxyOperations     []RepositoryOperation
	OCIDiscovery        *OCIDiscoveryProfile
}

// OCIDiscoveryProfile describes Gateway API support. Upstream enumeration
// coverage is reported by each Catalog/Referrers response, not this profile.
type OCIDiscoveryProfile struct {
	Catalog               bool
	Referrers             bool
	TagFallback           bool
	SignatureVerification bool
}

var supportedFormatProfiles = []FormatProfile{
	formatProfile(FormatOCI),
	formatProfile(FormatMaven),
	formatProfile(FormatConan),
	formatProfile(FormatRaw),
	{
		Format:              FormatNPM,
		RepositoryTypes:     []RepositoryType{RepositoryTypeHosted, RepositoryTypeProxy},
		GroupSupported:      true,
		AnonymousRead:       true,
		PublicationScanning: true,
		HostedOperations: []RepositoryOperation{
			RepositoryOperationDeprecate,
			RepositoryOperationRead,
			RepositoryOperationPublish,
			RepositoryOperationBrowse,
			RepositoryOperationDelete,
			RepositoryOperationRestore,
			RepositoryOperationRetain,
			RepositoryOperationReclaim,
			RepositoryOperationPromote,
			RepositoryOperationReplicate,
		},
		ProxyOperations: []RepositoryOperation{
			RepositoryOperationRead,
			RepositoryOperationBrowse,
		},
	},
	{
		Format:              FormatPyPI,
		RepositoryTypes:     []RepositoryType{RepositoryTypeHosted, RepositoryTypeProxy},
		GroupSupported:      true,
		AnonymousRead:       true,
		PublicationScanning: true,
		HostedOperations: []RepositoryOperation{
			RepositoryOperationYank,
			RepositoryOperationRead,
			RepositoryOperationPublish,
			RepositoryOperationBrowse,
			RepositoryOperationDelete,
			RepositoryOperationRestore,
			RepositoryOperationRetain,
			RepositoryOperationReclaim,
			RepositoryOperationPromote,
			RepositoryOperationReplicate,
		},
		ProxyOperations: []RepositoryOperation{
			RepositoryOperationRead,
			RepositoryOperationBrowse,
			RepositoryOperationReclaim,
		},
	},
	{
		Format:              FormatGo,
		RepositoryTypes:     []RepositoryType{RepositoryTypeHosted, RepositoryTypeProxy},
		GroupSupported:      true,
		AnonymousRead:       true,
		PublicationScanning: true,
		HostedOperations: []RepositoryOperation{
			RepositoryOperationRead,
			RepositoryOperationPublish,
			RepositoryOperationBrowse,
			RepositoryOperationDelete,
			RepositoryOperationRestore,
			RepositoryOperationRetain,
			RepositoryOperationReclaim,
			RepositoryOperationPromote,
			RepositoryOperationReplicate,
		},
		ProxyOperations: []RepositoryOperation{
			RepositoryOperationRead,
			RepositoryOperationBrowse,
		},
	},
	{
		Format:              FormatCargo,
		RepositoryTypes:     []RepositoryType{RepositoryTypeHosted, RepositoryTypeProxy},
		GroupSupported:      true,
		AnonymousRead:       true,
		PublicationScanning: true,
		HostedOperations: []RepositoryOperation{
			RepositoryOperationRead,
			RepositoryOperationPublish,
			RepositoryOperationBrowse,
			RepositoryOperationDelete,
			RepositoryOperationRestore,
			RepositoryOperationRetain,
			RepositoryOperationReclaim,
			RepositoryOperationPromote,
			RepositoryOperationReplicate,
		},
		ProxyOperations: []RepositoryOperation{
			RepositoryOperationRead,
			RepositoryOperationBrowse,
		},
	},
	{
		Format:              FormatAPT,
		PublicationScanning: true,
		RepositoryTypes:     []RepositoryType{RepositoryTypeProxy},
		GroupSupported:      true,
		AnonymousRead:       true,
		ProxyOperations: []RepositoryOperation{
			RepositoryOperationRead,
			RepositoryOperationBrowse,
		},
	},
}

func formatProfile(format Format) FormatProfile {
	profile := FormatProfile{
		Format:              format,
		RepositoryTypes:     []RepositoryType{RepositoryTypeHosted, RepositoryTypeProxy},
		GroupSupported:      true,
		AnonymousRead:       true,
		PublicationScanning: true,
		HostedOperations: []RepositoryOperation{
			RepositoryOperationRead,
			RepositoryOperationPublish,
			RepositoryOperationBrowse,
			RepositoryOperationDelete,
			RepositoryOperationRestore,
			RepositoryOperationRetain,
			RepositoryOperationReclaim,
			RepositoryOperationPromote,
			RepositoryOperationReplicate,
		},
		ProxyOperations: []RepositoryOperation{
			RepositoryOperationRead,
			RepositoryOperationBrowse,
			RepositoryOperationReclaim,
		},
	}
	if format == FormatOCI {
		profile.OCIDiscovery = &OCIDiscoveryProfile{Catalog: true, Referrers: true}
	}
	return profile
}

// SupportedFormatProfiles returns a defensive copy in display order.
func SupportedFormatProfiles() []FormatProfile {
	profiles := make([]FormatProfile, len(supportedFormatProfiles))
	for index, profile := range supportedFormatProfiles {
		profiles[index] = cloneFormatProfile(profile)
	}
	return profiles
}

// SupportedFormats returns every admitted format in display order.
func SupportedFormats() []Format {
	formats := make([]Format, 0, len(supportedFormatProfiles))
	for _, profile := range supportedFormatProfiles {
		formats = append(formats, profile.Format)
	}
	return formats
}

// WorkerFormats returns formats with executable background work. APT and Go
// are included for internal recovery/reclaim workers even though those
// operations are not advertised as user-facing lifecycle capabilities.
func WorkerFormats() []Format {
	formats := make([]Format, 0, len(supportedFormatProfiles))
	for _, profile := range supportedFormatProfiles {
		if profile.Format == FormatAPT || profile.Format == FormatGo || hasBackgroundOperation(profile.HostedOperations) || hasBackgroundOperation(profile.ProxyOperations) {
			formats = append(formats, profile.Format)
		}
	}
	return formats
}

func hasBackgroundOperation(operations []RepositoryOperation) bool {
	for _, operation := range operations {
		switch operation {
		case RepositoryOperationRetain, RepositoryOperationReclaim, RepositoryOperationPromote, RepositoryOperationReplicate:
			return true
		}
	}
	return false
}

func FormatProfileFor(format Format) (FormatProfile, bool) {
	for _, profile := range supportedFormatProfiles {
		if profile.Format == format {
			return cloneFormatProfile(profile), true
		}
	}
	return FormatProfile{}, false
}

func IsSupportedFormat(format Format) bool {
	_, ok := FormatProfileFor(format)
	return ok
}

func FormatSupportsRepositoryType(format Format, repositoryType RepositoryType) bool {
	profile, ok := FormatProfileFor(format)
	if !ok {
		return false
	}
	for _, candidate := range profile.RepositoryTypes {
		if candidate == repositoryType {
			return true
		}
	}
	return false
}

// FormatSupportsRepositoryProvisioning is the create-time admission boundary.
// APT Hosted is deliberately provisionable for the pre-visibility publication
// management workflow before its native read protocol is advertised. It stays
// absent from FormatProfile.RepositoryTypes until signed snapshot publication
// is complete.
func FormatSupportsRepositoryProvisioning(format Format, repositoryType RepositoryType) bool {
	return FormatSupportsRepositoryType(format, repositoryType) ||
		format == FormatAPT && repositoryType == RepositoryTypeHosted
}

func FormatSupportsOperation(format Format, repositoryType RepositoryType, operation RepositoryOperation) bool {
	profile, ok := FormatProfileFor(format)
	if !ok {
		return false
	}
	operations := profile.HostedOperations
	if repositoryType == RepositoryTypeProxy {
		operations = profile.ProxyOperations
	} else if repositoryType != RepositoryTypeHosted {
		return false
	}
	for _, candidate := range operations {
		if candidate == operation {
			return true
		}
	}
	return false
}

// FormatSupportsPublicationScanning reports whether a Hosted publication has a
// native immutable-asset resolver and can participate in scan reconciliation.
func FormatSupportsPublicationScanning(format Format, repositoryType RepositoryType) bool {
	profile, ok := FormatProfileFor(format)
	return ok && repositoryType == RepositoryTypeHosted && profile.PublicationScanning
}

func cloneFormatProfile(profile FormatProfile) FormatProfile {
	profile.RepositoryTypes = append([]RepositoryType(nil), profile.RepositoryTypes...)
	profile.HostedOperations = append([]RepositoryOperation(nil), profile.HostedOperations...)
	profile.ProxyOperations = append([]RepositoryOperation(nil), profile.ProxyOperations...)
	if profile.OCIDiscovery != nil {
		discovery := *profile.OCIDiscovery
		profile.OCIDiscovery = &discovery
	}
	return profile
}
