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
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

// DefaultMIContainerCount is the assumed MI container pool size when
// the LEASED_MSI_CONTAINERS envvar is not set. It matches the typical
// personal-dev environment slot count so local runs work out of the box.
const DefaultMIContainerCount = 15

func parseMIContainersLabel(spec *et.ExtensionTestSpec) (int, bool) {
	var seen []string
	var parsed int
	var found bool

	for label := range spec.Labels {
		if v, ok := strings.CutPrefix(label, "MIContainers:"); ok {
			seen = append(seen, label)
			n, err := strconv.Atoi(v)
			if err == nil {
				if n < 0 {
					fmt.Fprintf(os.Stderr, "FATAL: test %q has MIContainers:%d but N must be >= 0\n", spec.Name, n)
					os.Exit(1)
				}
				parsed = n
				found = true
			}
		}
	}

	if len(seen) > 1 {
		sort.Strings(seen)
		fmt.Fprintf(os.Stderr, "FATAL: test %q has multiple MIContainers labels (%s); exactly one is required\n",
			spec.Name, strings.Join(seen, ", "))
		os.Exit(1)
	}
	if found {
		return parsed, true
	}
	return 0, false
}

// parseMIContainerCount returns the MI container pool size and a
// human-readable source string. The pool size is the number of
// space-delimited entries in LEASED_MSI_CONTAINERS, or
// DefaultMIContainerCount when the envvar is unset or empty.
func parseMIContainerCount() (int, string) {
	v := os.Getenv(leaseenv.MSIContainers)
	if v == "" {
		return DefaultMIContainerCount, fmt.Sprintf("default (%s not set)", leaseenv.MSIContainers)
	}
	count := len(strings.Fields(v))
	if count == 0 {
		return DefaultMIContainerCount, fmt.Sprintf("default (%s empty)", leaseenv.MSIContainers)
	}
	return count, leaseenv.MSIContainers
}

func miDemandPriority(spec *et.ExtensionTestSpec) int {
	demand, _ := parseMIContainersLabel(spec)
	return demand
}

type miSchedulerConfig struct {
	pooledIdentitiesEnabled bool
	containerCount          int
	containerCountSource    string
}

var (
	miSchedulerSetup     miSchedulerConfig
	miSchedulerSpecs     et.ExtensionTestSpecs
	miSchedulerConfigure sync.Once
)

func initMIScheduler(specs et.ExtensionTestSpecs, cfg miSchedulerConfig) {
	miSchedulerSpecs = specs
	miSchedulerSetup = cfg
}

// configureMIScheduler walks specs to wire up per-test MI container demands for the
// openshift-tests-extension resource-aware scheduler. Each spec's MIContainers(N) label
// declares how many pooled identity containers it will lease. When pooled identities are
// enabled, we set spec.Resources.ResourcePools["mi-containers"] = N so the scheduler
// won't start the test until N slots are free in the pool.
func configureMIScheduler(specs et.ExtensionTestSpecs, cfg miSchedulerConfig) {
	var missingLabel []string
	var demand0, demand1, demandN int
	specs.Walk(func(spec *et.ExtensionTestSpec) {
		demand, ok := parseMIContainersLabel(spec)
		if !ok {
			missingLabel = append(missingLabel, spec.Name)
			return
		}
		switch demand {
		case 0:
			demand0++
		case 1:
			demand1++
		default:
			demandN++
		}
		if cfg.pooledIdentitiesEnabled && demand > 0 {
			if spec.Resources.ResourcePools == nil {
				spec.Resources.ResourcePools = make(map[string]int)
			}
			spec.Resources.ResourcePools["mi-containers"] = demand
		}
	})
	if len(missingLabel) > 0 {
		fmt.Fprintf(os.Stderr, "FATAL: %d tests missing MIContainers label:\n", len(missingLabel))
		for _, name := range missingLabel {
			fmt.Fprintf(os.Stderr, "  - %s\n", name)
		}
		os.Exit(1)
	}
	total := demand0 + demand1 + demandN
	if cfg.pooledIdentitiesEnabled {
		fmt.Fprintf(os.Stderr, "[scheduler] pool mi-containers=%d (source: %s), %d specs (%d×0, %d×1, %d×2+)\n",
			cfg.containerCount, cfg.containerCountSource, total, demand0, demand1, demandN)
	} else {
		fmt.Fprintf(os.Stderr, "[scheduler] pooled identities disabled (%s!=true), skipping mi-containers pool demands; %d specs (%d×0, %d×1, %d×2+)\n",
			framework.UsePooledIdentitiesEnvvar, total, demand0, demand1, demandN)
	}

	if os.Getenv("ARO_HCP_DISABLE_MI_SORT") != "true" {
		sort.SliceStable(specs, func(i, j int) bool {
			return miDemandPriority(specs[i]) > miDemandPriority(specs[j])
		})
	}
}

func ensureMISchedulerConfigured() {
	miSchedulerConfigure.Do(func() {
		configureMIScheduler(miSchedulerSpecs, miSchedulerSetup)
	})
}
