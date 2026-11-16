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

// Package expiry classifies credential expiry times against a warning window.
package expiry

import "time"

// DefaultWarning is how long before expiry a credential starts being reported
// as expiring.
const DefaultWarning = 90 * 24 * time.Hour

// State is how close a credential is to its expiry.
type State int

const (
	// Valid means the credential expires after the warning window.
	Valid State = iota
	// Expiring means the credential is still valid but expires within the warning window.
	Expiring
	// Expired means the credential is no longer valid.
	Expired
)

// Classify reports whether a credential ending at end is expired at now, or
// expires within warning of now. A credential is expired from the instant it ends.
func Classify(end, now time.Time, warning time.Duration) State {
	switch {
	case !end.After(now):
		return Expired
	case end.Sub(now) <= warning:
		return Expiring
	default:
		return Valid
	}
}

// Days returns the whole days from now until end, rounding toward zero;
// negative once end has passed.
func Days(end, now time.Time) int {
	return int(end.Sub(now) / (24 * time.Hour))
}
