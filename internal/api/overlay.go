// Copyright 2025 Microsoft Corporation
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

package api

// ApplyVersionedUpdate applies the versioned external object onto an existing
// internal object. For UPDATE paths (PUT-replace and PATCH).
// The caller must provide a deep copy of the existing internal object as 'base'.
//
// Procedure:
//  1. version.ZeroOwnedFields(base)  -- zero only this version's fields
//  2. version.ApplyOwnedFields(base) -- copy request values into zeroed positions
//
// Fields not in this version's owned set retain their stored values from 'base'.
func ApplyVersionedUpdate[T any](
	versioned VersionedCreatableResource[T],
	base *T,
) error {
	versioned.ZeroOwnedFields(base)
	return versioned.ApplyOwnedFields(base)
}

// ApplyVersionedCreate applies the versioned external object onto a fresh
// internal default object. For CREATE paths (PUT-create and preflight).
// The caller must provide a freshly constructed default object as 'base'
// and call EnsureDefaults() after this function returns.
func ApplyVersionedCreate[T any](
	versioned VersionedCreatableResource[T],
	base *T,
) error {
	versioned.ZeroOwnedFields(base)
	return versioned.ApplyOwnedFields(base)
}
