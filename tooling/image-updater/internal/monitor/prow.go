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

package monitor

import (
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/prowjobs"
)

// Reuse the tenant-quota Prow client so both tools share its streaming
// decode, retries and URL validation; fixes land once.
type ProwClient = prowjobs.Client

// ProwJob is the Prow job schema; the monitor reads job name, result,
// timing and URL.
type ProwJob = prowjobs.Job

// NewProwClient returns a ProwClient that reads {baseURL}/prowjobs.js.
func NewProwClient(baseURL string, timeout time.Duration) ProwClient {
	return prowjobs.NewHTTPClient(strings.TrimSpace(baseURL), timeout)
}
