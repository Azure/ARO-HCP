// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package akslog provides structured logging helpers used by ARO HCP pipeline
// scripts. Logs are emitted as JSON to stderr so the Geneva collector ships
// them to Kusto in the same shape as backend and other first-party services.
package akslog

import (
	"fmt"
	"log/slog"
	"strings"
)

// Logf logs a structured message, routing WARN:/ERROR: prefixes to the
// appropriate slog level.
func Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	switch {
	case strings.HasPrefix(msg, "WARN:"):
		slog.Warn(strings.TrimSpace(strings.TrimPrefix(msg, "WARN:")))
	case strings.HasPrefix(msg, "ERROR:"):
		slog.Error(strings.TrimSpace(strings.TrimPrefix(msg, "ERROR:")))
	default:
		slog.Info(msg)
	}
}

// Banner emits a visual section divider. The phase name is recorded as a
// structured attribute so Kusto queries can filter on it directly.
func Banner(s string) {
	slog.Info(strings.Repeat("=", 60), "phase", s)
	slog.Info(">>> "+s, "phase", s)
	slog.Info(strings.Repeat("=", 60), "phase", s)
}

// Buffer logs a multi-line string line-by-line, prefixed by label.
func Buffer(label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, line := range strings.Split(value, "\n") {
		Logf("%s: %s", label, line)
	}
}

// Deref safely dereferences a *string, returning "" for nil.
func Deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
