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
	ginkgo "github.com/onsi/ginkgo/v2"
)

// Positivity of test cases
var (
	Positive = ginkgo.Label("Positive")
	Negative = ginkgo.Label("Negative")
)

// Importance of test cases
var (
	Low      = ginkgo.Label("Low")
	Medium   = ginkgo.Label("Medium")
	High     = ginkgo.Label("High")
	Critical = ginkgo.Label("Critical")
)

// Usage of test cases
var (
	CoreInfraService   = ginkgo.Label("Core-Infra-Service")
	CreateCluster      = ginkgo.Label("Create-Cluster")
	SetupValidation    = ginkgo.Label("Setup-Validation")
	TeardownValidation = ginkgo.Label("Teardown-Validation")
)
