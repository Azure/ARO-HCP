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

package main

import (
	"fmt"
	"time"

	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

func fastTestsOnly(query string) string {
	return fmt.Sprintf("(%s) && !labels.exists(l, l==\"%s\")", query, labels.Slow[0])
}

func slowTestsOnly(query string) string {
	return fmt.Sprintf("(%s) && labels.exists(l, l==\"%s\")", query, labels.Slow[0])
}

// Register suite definitions with resolved worker counts and independent MI resource budgets.
func registerSuites(ext *e.Extension, miPools map[string]int, upgradeInPlaceCount int, workers suiteConcurrency) {
	// Remember that the label constants are (currently) slices, not items.

	// The tests that a suite is composed of can be filtered by CEL expressions. By
	// default, the qualifiers only apply to tests from this extension.
	integrationQuery := fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.DevelopmentOnly[0], labels.StageAndProdOnly[0], labels.HypershiftPresubmit[0])
	integrationTestTimeout := 150 * time.Minute
	ext.AddSuite(e.Suite{
		Name: "integration/parallel",
		Qualifiers: []string{
			fastTestsOnly(integrationQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Ordinary suites opt in to the acquired lease count unless the user overrides it.
		Parallelism:   workers.resolve(24, true),
		TestTimeout:   &integrationTestTimeout,
		ResourcePools: miPools,
	})

	ext.AddSuite(e.Suite{
		Name: "integration/parallel/slow",
		Qualifiers: []string{
			slowTestsOnly(integrationQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Ordinary suites opt in to the acquired lease count unless the user overrides it.
		Parallelism:   workers.resolve(24, true),
		TestTimeout:   &integrationTestTimeout,
		ResourcePools: miPools,
	})
	ext.AddSuite(e.Suite{
		Name:       "integration/parallel/all",
		Qualifiers: []string{integrationQuery},
		// Keep the same resource limits as the existing Integration suites.
		Parallelism:   workers.resolve(24, true),
		TestTimeout:   &integrationTestTimeout,
		ResourcePools: miPools,
	})

	stageQuery := fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.IntegrationOnly[0], labels.DevelopmentOnly[0], labels.HypershiftPresubmit[0])
	stageTestTimeout := 150 * time.Minute
	ext.AddSuite(e.Suite{
		Name: "stage/parallel",
		Qualifiers: []string{
			fastTestsOnly(stageQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Ordinary suites opt in to the acquired lease count unless the user overrides it.
		Parallelism:   workers.resolve(34, true),
		TestTimeout:   &stageTestTimeout,
		ResourcePools: miPools,
	})
	ext.AddSuite(e.Suite{
		Name: "stage/parallel/slow",
		Qualifiers: []string{
			slowTestsOnly(stageQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Ordinary suites opt in to the acquired lease count unless the user overrides it.
		Parallelism:   workers.resolve(34, true),
		TestTimeout:   &stageTestTimeout,
		ResourcePools: miPools,
	})
	ext.AddSuite(e.Suite{
		Name:       "stage/parallel/all",
		Qualifiers: []string{stageQuery},
		// Keep the same resource limits as the existing Stage suites.
		Parallelism:   workers.resolve(34, true),
		TestTimeout:   &stageTestTimeout,
		ResourcePools: miPools,
	})

	prodQuery := fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.IntegrationOnly[0], labels.DevelopmentOnly[0], labels.HypershiftPresubmit[0])
	prodTestTimeout := 150 * time.Minute
	ext.AddSuite(e.Suite{
		Name: "prod/parallel",
		Qualifiers: []string{
			fastTestsOnly(prodQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Ordinary suites opt in to the acquired lease count unless the user overrides it.
		Parallelism:   workers.resolve(19, true),
		TestTimeout:   &prodTestTimeout,
		ResourcePools: miPools,
	})
	ext.AddSuite(e.Suite{
		Name: "prod/parallel/slow",
		Qualifiers: []string{
			slowTestsOnly(prodQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Ordinary suites opt in to the acquired lease count unless the user overrides it.
		Parallelism:   workers.resolve(19, true),
		TestTimeout:   &prodTestTimeout,
		ResourcePools: miPools,
	})

	ext.AddSuite(e.Suite{
		Name: "dev-cd-check/parallel",
		Qualifiers: []string{
			// Subset of E2E tests to be executed as a final step during ARO
			// HCP Continous Deployment GitHub Action Workflow.
			// TODO: revisit labels to tweak which tests to select here
			fastTestsOnly(fmt.Sprintf(`labels.exists(l, l=="%s" ) && labels.exists(l, l=="%s")`, labels.AroRpApiCompatible[0], labels.Positive[0])),
		},
		// Only an explicit user override replaces this specialized default.
		Parallelism:   workers.resolve(20, false),
		ResourcePools: miPools,
	})

	rpApiCompatBaseQualifier := fmt.Sprintf(`labels.exists(l, l=="%s")`, labels.AroRpApiCompatible[0])

	if framework.IsDevelopmentEnvironment() {
		rpApiCompatBaseQualifier = fmt.Sprintf(`%s || labels.exists(l, l=="%s")`, rpApiCompatBaseQualifier, labels.DevelopmentOnly[0])
	} else {
		rpApiCompatBaseQualifier = fmt.Sprintf(`%s && !labels.exists(l, l=="%s")`, rpApiCompatBaseQualifier, labels.DevelopmentOnly[0])
	}

	rpApiCompatTestTimeout := 150 * time.Minute
	ext.AddSuite(e.Suite{
		Name:       "rp-api-compat-all/parallel",
		Qualifiers: []string{fastTestsOnly(rpApiCompatBaseQualifier)},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		Parallelism:   workers.resolve(24, true),
		TestTimeout:   &rpApiCompatTestTimeout,
		ResourcePools: miPools,
	})
	ext.AddSuite(e.Suite{
		Name:       "rp-api-compat-all/parallel/slow",
		Qualifiers: []string{slowTestsOnly(rpApiCompatBaseQualifier)},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		Parallelism:   workers.resolve(24, true),
		TestTimeout:   &rpApiCompatTestTimeout,
		ResourcePools: miPools,
	})

	hypershiftPresubmitQuery := fmt.Sprintf(`labels.exists(l, l=="%s")`, labels.HypershiftPresubmit[0])
	hypershiftPresubmitTimeout := 150 * time.Minute
	ext.AddSuite(e.Suite{
		Name:          "hypershift-presubmit/parallel",
		Qualifiers:    []string{hypershiftPresubmitQuery},
		Parallelism:   workers.resolve(24, true),
		TestTimeout:   &hypershiftPresubmitTimeout,
		ResourcePools: miPools,
	})

	// upgrade/in-place runs UpgradeInPlace specs in parallel. Each spec provisions
	// its own cluster+nodepool and captures a baseline, then all specs synchronise
	// at an UpgradeBarrier while the UpgradeCoordinator (parent run-suite process)
	// runs the Region entrypoint pipeline once for the suite. After the upgrade every
	// spec validates its own cluster independently (hash, haproxy image, DataSecretName).
	//
	// Default workers match the UpgradeInPlace participant count, independently
	// of acquired identity capacity. Explicit worker overrides must permit every
	// participant to start; fewer workers deadlock at the barrier waiting for
	// queued participants.
	upgradeInPlaceTimeout := 120 * time.Minute
	ext.AddSuite(e.Suite{
		Name: "upgrade/in-place",
		Qualifiers: []string{
			fmt.Sprintf(`labels.exists(l, l=="%s")`, labels.UpgradeInPlace[0]),
		},
		Parallelism: workers.resolve(upgradeInPlaceCount, false),
		TestTimeout: &upgradeInPlaceTimeout,
	})
}
