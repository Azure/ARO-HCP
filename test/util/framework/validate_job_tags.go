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

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onsi/gomega"

	"k8s.io/utils/ptr"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func (tc *perItOrDescribeTestContext) validateCreatedHCPJobTags(ctx context.Context) {
	expected := WithJobTags(nil)
	if len(expected) == 0 {
		return
	}
	tc.contextLock.RLock()
	resourceGroups := slices.Clone(tc.knownResourceGroups)
	tc.contextLock.RUnlock()
	if len(resourceGroups) == 0 {
		return
	}
	startedAt := time.Now()
	defer func() {
		tc.RecordTestStep("Validate HCP resource job tags", startedAt, time.Now())
	}()
	factory, err := tc.Get20240610ClientFactory(ctx)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to get HCP clients for job-tag validation")
	err = validateHCPJobTags20240610(ctx, factory, resourceGroups, expected)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "test-created HCP resources must carry the configured job tags; add them explicitly with framework.WithJobTags or the Bicep jobTags parameter")
}

// Validate only Microsoft.RedHatOpenShift tracked resources. External auths are
// proxy resources without tags. Listing through the HCP SDK also works directly
// against the RP in development, where ARM's generic inventory lacks HCPs.
func validateHCPJobTags20240610(ctx context.Context, factory *hcpsdk20240610preview.ClientFactory, resourceGroups []string, expected map[string]*string) error {
	var errs []error
	for _, resourceGroup := range resourceGroups {
		clusters := factory.NewHcpOpenShiftClustersClient().NewListByResourceGroupPager(resourceGroup, nil)
		for clusters.More() {
			page, err := clusters.NextPage(ctx)
			if err != nil {
				if !IsNotFoundError(err) {
					errs = append(errs, fmt.Errorf("list clusters in %s for job-tag validation: %w", resourceGroup, err))
				}
				break
			}
			for _, cluster := range page.Value {
				if cluster == nil {
					continue
				}
				name := ptr.Deref(cluster.Name, "")
				resource := ptr.Deref(cluster.ID, resourceGroup+"/hcpOpenShiftClusters/"+name)
				errs = append(errs, validateResourceJobTags(resource, cluster.Tags, expected))
				nodePools := factory.NewNodePoolsClient().NewListByParentPager(resourceGroup, name, nil)
				for nodePools.More() {
					page, err := nodePools.NextPage(ctx)
					if err != nil {
						if !IsNotFoundError(err) {
							errs = append(errs, fmt.Errorf("list node pools in %s for job-tag validation: %w", resource, err))
						}
						break
					}
					for _, nodePool := range page.Value {
						if nodePool == nil {
							continue
						}
						id := ptr.Deref(nodePool.ID, resource+"/nodePools/"+ptr.Deref(nodePool.Name, ""))
						errs = append(errs, validateResourceJobTags(id, nodePool.Tags, expected))
					}
				}
			}
		}
	}
	return errors.Join(errs...)
}

func validateResourceJobTags(resource string, tags, expected map[string]*string) error {
	var errs []error
	for _, key := range []string{ProwJobNameTag, ProwJobIDTag} {
		want, required := expected[key]
		if !required {
			continue
		}
		var got *string
		for tag, value := range tags {
			if strings.EqualFold(tag, key) {
				got = value
				break
			}
		}
		if got == nil {
			errs = append(errs, fmt.Errorf("%s: missing tag %q (expected %q)", resource, key, *want))
		} else if *got != *want {
			errs = append(errs, fmt.Errorf("%s: tag %q is %q, expected %q", resource, key, *got, *want))
		}
	}
	return errors.Join(errs...)
}
