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
)

var (
	DevelopmentOnly = ginkgo.Label("Development-Only")
	IntegrationOnly = ginkgo.Label("Integration-Only")
	// A test case is ARO-HCP-RP-API-Compatible if it doesn't use ARM API (eg.
	// ARM templates) to communicate with ARO HCP RP, so that it can run
	// against either ARO HCP RP or ARM endpoint.
	AroRpApiCompatible = ginkgo.Label("ARO-HCP-RP-API-Compatible")
)

// Environments this test can be used in.
var (
	RequireNothing        = ginkgo.Label("PreLaunchSetup:None")
	RequireHappyPathInfra = ginkgo.Label("PreLaunchSetup:HappyPathInfra")
)
