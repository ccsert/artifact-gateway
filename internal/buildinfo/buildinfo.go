package buildinfo

import (
	"runtime"
	"runtime/debug"
)

var (
	injectedVersion  string
	injectedRevision string
)

type Info struct {
	Version   string
	Revision  string
	Modified  bool
	GoVersion string
}

func Read() Info {
	result := Info{Version: "dev", Revision: "unknown", GoVersion: runtime.Version()}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			result.Version = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if setting.Value != "" {
					result.Revision = setting.Value
				}
			case "vcs.modified":
				result.Modified = setting.Value == "true"
			}
		}
	}
	if injectedVersion != "" {
		result.Version = injectedVersion
	}
	if injectedRevision != "" {
		result.Revision = injectedRevision
	}
	return result
}
