package util

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

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
