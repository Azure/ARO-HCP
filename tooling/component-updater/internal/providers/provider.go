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

package providers

import "context"

// VersionProvider discovers whether a configured component version can advance.
// Implementations use only the CheckRequest fields they need; unused fields stay zero.
type VersionProvider interface {
	Check(ctx context.Context, req CheckRequest) (Result, error)
}

// UpdateValidator is an optional hook for providers that gate writes (for example
// refusing a shared-default bump from a regional subset). Providers that do not
// implement it are treated as always eligible to write after a successful Check.
type UpdateValidator interface {
	ValidateUpdate(req UpdateRequest) error
}

// CheckRequest is the neutral discovery input shared by all providers.
type CheckRequest struct {
	// Current is the configured version read from the target file.
	Current string

	// Locations optionally scopes regional discovery. Empty for non-regional providers.
	Locations []string

	// ConstraintVersion is an optional second configured value some providers
	// use when selecting a candidate (for azure-istio: the configured AKS version).
	ConstraintVersion string
}

// UpdateRequest carries pre-write validation inputs. Providers ignore unused fields.
type UpdateRequest struct {
	Locations []string
}

type Result struct {
	Available     []string
	Candidate     string
	Compatibility string
	Next          string
	Detail        string
	Regions       []RegionResult
}

type RegionResult struct {
	Location   string
	Current    string
	Compatible []string
	Upgrades   []string
	Candidate  string
	Detail     string
}
