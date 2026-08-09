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
)

// FormatProfile is the capability contract for one supported artifact format.
// Protocol routing and persistence remain format-owned; this profile controls
// admission and capability discovery across management surfaces.
type FormatProfile struct {
	Format           Format
	RepositoryTypes  []RepositoryType
	GroupSupported   bool
	AnonymousRead    bool
	HostedOperations []RepositoryOperation
	ProxyOperations  []RepositoryOperation
}

var supportedFormatProfiles = []FormatProfile{
	formatProfile(FormatOCI),
	formatProfile(FormatMaven),
	formatProfile(FormatConan),
	formatProfile(FormatRaw),
	{
		Format:          FormatNPM,
		RepositoryTypes: []RepositoryType{RepositoryTypeHosted, RepositoryTypeProxy},
		GroupSupported:  true,
		AnonymousRead:   true,
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
		Format:          FormatPyPI,
		RepositoryTypes: []RepositoryType{RepositoryTypeHosted, RepositoryTypeProxy},
		GroupSupported:  true,
		AnonymousRead:   true,
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
	},
}

func formatProfile(format Format) FormatProfile {
	return FormatProfile{
		Format:          format,
		RepositoryTypes: []RepositoryType{RepositoryTypeHosted, RepositoryTypeProxy},
		GroupSupported:  true,
		AnonymousRead:   true,
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

// WorkerFormats returns formats with at least one declared asynchronous
// lifecycle operation. Protocol-only formats must not be scheduled by generic
// background workers until those capabilities are implemented.
func WorkerFormats() []Format {
	formats := make([]Format, 0, len(supportedFormatProfiles))
	for _, profile := range supportedFormatProfiles {
		if hasBackgroundOperation(profile.HostedOperations) || hasBackgroundOperation(profile.ProxyOperations) {
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

func cloneFormatProfile(profile FormatProfile) FormatProfile {
	profile.RepositoryTypes = append([]RepositoryType(nil), profile.RepositoryTypes...)
	profile.HostedOperations = append([]RepositoryOperation(nil), profile.HostedOperations...)
	profile.ProxyOperations = append([]RepositoryOperation(nil), profile.ProxyOperations...)
	return profile
}
