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

package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Node pool create validation", func() {
	It("rejects a negative replica count without creating a node pool or changing the shared cluster",
		labels.RequireSharedReadOnlyCluster,
		labels.High,
		labels.Negative,
		labels.AroRpApiCompatible,
		labels.MIContainers(0),
		func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			manifest, reader, err := framework.SharedReadOnlyCluster(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to borrow the run-owned fixture for node pool create validation")

			By("reusing the shared node pool's valid platform and version without modifying it")
			baseline, err := reader.GetNodePool20240610(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to read the owner-provisioned node pool")
			Expect(baseline.Properties).NotTo(BeNil(), "shared node pool properties were nil")
			Expect(baseline.Properties.ProvisioningState).NotTo(BeNil(), "shared node pool provisioning state was nil")
			Expect(*baseline.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ProvisioningStateSucceeded), "shared node pool must be Succeeded before reusing its configuration")
			Expect(baseline.Properties.Platform).NotTo(BeNil(), "shared node pool platform was nil")
			Expect(baseline.Properties.Platform.VMSize).NotTo(BeNil(), "shared node pool VM size was nil")
			Expect(baseline.Properties.Version).NotTo(BeNil(), "shared node pool version was nil")
			Expect(baseline.Properties.Version.ID).NotTo(BeNil(), "shared node pool version ID was nil")
			nodePool := hcpsdk20240610preview.NodePool{
				Location: to.Ptr(manifest.Location),
				Properties: &hcpsdk20240610preview.NodePoolProperties{
					Platform:   baseline.Properties.Platform,
					Version:    baseline.Properties.Version,
					AutoRepair: baseline.Properties.AutoRepair,
					Replicas:   to.Ptr[int32](-1),
				},
			}

			By("requiring typed child absence before and after a serialized invalid node pool CREATE")
			_, guarded, err := framework.SharedGuardedNegativeCluster(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to acquire the guarded-negative fixture handle")
			err = guarded.RejectNegativeNodePoolReplicas20240610(ctx, "invalid-"+rand.String(7), nodePool)
			Expect(err).NotTo(HaveOccurred(), "RP must reject properties.replicas with typed HTTP 400 / InvalidRequestContent, leave the new node pool absent, and preserve the shared cluster")
		},
	)
})
