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

package framework

import "time"

// When updating timeouts, please refer to test/e2e/README.md for instructions.
// Provisioning timeouts
const (
	ClusterCreationTimeout      = 20 * time.Minute
	NodePoolCreationTimeout     = 20 * time.Minute
	ExternalAuthCreationTimeout = 15 * time.Minute
)
