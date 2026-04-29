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

package cachedreader

// BackendIdentityAzureCachedReaders groups Azure cached readers used with the backend
// identity. The backend identity is used to interact with Red Hat side Azure infrastructure.
type BackendIdentityAzureCachedReaders struct {
	// RoleDefinitionsCachedReader returns role definitions (e.g. for resolving role
	// definition IDs to allowed actions) with in-memory caching on read paths.
	RoleDefinitionsCachedReader RoleDefinitionsCachedReader
}
