package util


import "runtime/debug"

func Version() string {
	version := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				version = setting.Value
				break
			}
		}
	}

	return version
}
