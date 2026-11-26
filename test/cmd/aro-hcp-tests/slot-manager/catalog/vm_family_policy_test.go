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

package catalog

import (
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/pkg/vmfamily"
)

func TestCatalogVMFamilyPolicyPoolIsolation(t *testing.T) {
	t.Parallel()
	catalog := loadCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
    - name: limited
      subscriptions: {e2e: limited-sub}
      region: westus3
      slot_count: 2
      slot_assets:
        e2e_identities:
          allocation: dedicated
          resource_group_prefix: limited-identities
          resource_group_count: 1
      vm_family_policy:
        worker_families: [standardDSv5Family]
        helper_families: [standardDDSv5Family]
    - name: unrestricted
      subscriptions: {e2e: unrestricted-sub}
      region: westus3
      slot_count: 1
      slot_assets:
        e2e_identities:
          allocation: dedicated
          resource_group_prefix: unrestricted-identities
          resource_group_count: 1
`)
	for _, subscription := range []string{"limited-sub", "unrestricted-sub"} {
		pool, err := catalog.ResolvePool("dev", sets.New(subscription), nil, "westus3")
		if err != nil {
			t.Fatal(err)
		}
		want := vmfamily.VMFamilyPolicy{}
		if subscription == "limited-sub" {
			want.WorkerFamilies = []string{"standardDSv5Family"}
			want.HelperFamilies = []string{"standardDDSv5Family"}
		}
		for _, slot := range ExpandSlotsForPool("dev", pool) {
			if got := slot.VMFamilyPolicy.Resolve("westus3"); !reflect.DeepEqual(got, want) {
				t.Fatalf("subscription %q slot %q: got %+v, want %+v", subscription, slot.ResourceName, got, want)
			}
		}
	}
}

func TestCatalogRejectsMalformedVMFamilyPolicy(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{
		`[]`,
		`{unknown: [standardDSv5Family]}`,
		`{worker_families: null}`,
		`{helper_families: null}`,
		`{worker_families: []}`,
		`{helper_families: []}`,
		`{worker_families: standardDSv5Family}`,
		`{worker_families: [null]}`,
		`{worker_families: [123]}`,
		`{worker_families: [""]}`,
		`{worker_families: [standardDSv5Family, standardDSv5Family]}`,
		`{worker_families: [standardDSv5Family], worker_families: [standardDDSv5Family]}`,
		`{regions: null}`,
		`{regions: []}`,
		`{regions: {westus3: null}}`,
		`{regions: {westus3: {helper_families: null}}}`,
		`{regions: {westus3: {worker_families: []}}}`,
		`{regions: {westus3: {unknown: [standardDSv5Family]}}}`,
		`{regions: {westus3: {regions: {}}}}`,
		`{regions: {"": {}}}`,
		`{regions: {"West US 3": {}}}`,
		`{regions: {westus3: {}, westus3: {}}}`,
	} {
		t.Run(policy, func(t *testing.T) {
			input := strings.Replace(dedicatedCatalog, "      slot_count: 2", "      slot_count: 2\n      vm_family_policy: "+policy, 1)
			if _, err := loadCatalogFromYAMLWithError(t, input); err == nil {
				t.Fatalf("malformed policy was accepted: %s", policy)
			}
		})
	}
}

func TestCatalogValidatesProgrammaticVMFamilyPolicy(t *testing.T) {
	t.Parallel()
	for _, policy := range []PoolVMFamilyPolicy{
		{VMFamilyPolicy: vmfamily.VMFamilyPolicy{WorkerFamilies: []string{}}},
		{Regions: map[string]vmfamily.VMFamilyPolicy{"westus3": {HelperFamilies: []string{}}}},
		{Regions: map[string]vmfamily.VMFamilyPolicy{" ": {}}},
	} {
		catalog := loadCatalogFromYAML(t, dedicatedCatalog)
		catalog.Environments["dev"].Pools[0].VMFamilyPolicy = policy
		if err := catalog.Validate(); err == nil {
			t.Fatalf("invalid programmatic policy was accepted: %+v", policy)
		}
	}
}
