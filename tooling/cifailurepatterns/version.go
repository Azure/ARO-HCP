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

package cifailurepatterns

// Version is the opaque identifier for this extractor's semantic behavior. It is
// fixed by the implementation; callers cannot override it or order versions by it.
// The Go module revision identifies source distribution, not extraction behavior.
// There is no version registry or hash catalog in this package.
//
// Reviewers must bump Version for any change to the returned Findings for
// identical Input.Text, Input.TestName, and Input.Source. This includes keys,
// canonical or evidence text, selection precedence, finding cardinality, outcome,
// rules or quality, provider or codes, subjects or observed names, and byte
// locations. Behavior-preserving refactors do not require a bump.
//
// Reusing an identifier requires restoring its matching implementation and
// reviewing it against the prior replay corpus. Storage, input fingerprints,
// replay, and reporting remain outside this pure extraction library.
const Version = "fp-1"
