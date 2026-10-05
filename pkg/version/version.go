package version

import "runtime/debug"

var (
	Version = "dev"
	Commit  = "unknown"
)

func String() string {
	commit := Commit
	if commit == "unknown" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" && len(s.Value) >= 7 {
					commit = s.Value[:7]
				}
			}
		}
	}
	return Version + " " + commit
}
