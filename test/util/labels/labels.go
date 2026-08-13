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

package labels

import (
	"fmt"

	"github.com/onsi/ginkgo/v2"
)

// TODO makes these ginkgo.Labels produced by ginkgo.Label (notice the plural return) into strings and refactor the test usage.

var (
	// Positivity of test cases
	Positive = ginkgo.Label("Positivity:Positive")
	Negative = ginkgo.Label("Positivity:Negative")

	Slow = ginkgo.Label("Speed:Slow")
)

// Importance of test cases
var (
	Low      = ginkgo.Label("Importance:Low")
	Medium   = ginkgo.Label("Importance:Medium")
	High     = ginkgo.Label("Importance:High")
	Critical = ginkgo.Label("Importance:Critical")
)

// Usage of test cases
var (
	CoreInfraService   = ginkgo.Label("Core-Infra-Service")
	CreateCluster      = ginkgo.Label("Create-Cluster")
	SetupValidation    = ginkgo.Label("Setup-Validation")
	TeardownValidation = ginkgo.Label("Teardown-Validation")
	// UpgradeInPlace marks the end-to-end in-place Hypershift upgrade test. Selected
	// exclusively by the upgrade/in-place suite — not part of any happy-path or
	// api-compat suite.
	UpgradeInPlace = ginkgo.Label("Upgrade-In-Place")
	// HypershiftPresubmit marks tests that validate Control Plane Operator (CPO)
	// behavior. Selected by the hypershift-presubmit/parallel suite so that
	// HyperShift presubmit PRs can run a targeted subset of ARO-HCP e2e tests.
	HypershiftPresubmit = ginkgo.Label("Hypershift-Presubmit")
)

var (
	DevelopmentOnly  = ginkgo.Label("Development-Only")
	IntegrationOnly  = ginkgo.Label("Integration-Only")
	StageAndProdOnly = ginkgo.Label("Stage-And-Prod-Only")
	// A test case is ARO-HCP-RP-API-Compatible if it doesn't use ARM API (eg.
	// ARM templates) to communicate with ARO HCP RP, so that it can run
	// against either ARO HCP RP or ARM endpoint.
	AroRpApiCompatible = ginkgo.Label("ARO-HCP-RP-API-Compatible")
	// AllowRetry marks a test as safe to auto-retry during an EV2 Stage/Prod
	// gating run when it fails due to a known, actively tracked issue. This is
	// a temporary measure with a TTL: every use must have an owner and a
	// tracking issue, and the label must be removed once the underlying issue
	// is fixed. See AROSLSRE-1721.
	AllowRetry = ginkgo.Label("allow-retry")
)

// MIContainers declares how many managed identity containers a test needs.
// Every It() and DescribeTable() must include this label. The verify-mi-containers
// CI check and the runtime scheduler enforce this — unlabeled tests are rejected.
func MIContainers(n int) ginkgo.Labels {
	if n < 0 {
		panic(fmt.Sprintf("MIContainers: n must be >= 0, got %d", n))
	}
	return ginkgo.Label(fmt.Sprintf("MIContainers:%d", n))
}

// Environments this test can be used in.
var (
	RequireNothing        = ginkgo.Label("PreLaunchSetup:None")
	RequireHappyPathInfra = ginkgo.Label("PreLaunchSetup:HappyPathInfra")
)
