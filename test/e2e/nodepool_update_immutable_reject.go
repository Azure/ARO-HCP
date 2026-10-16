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
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Immutable node pool configuration", func() {
	It("rejects an immutable node pool OS disk size PATCH without changing the shared node pool",
		labels.RequireSharedReadOnlyCluster,
		labels.High,
		labels.Negative,
		labels.AroRpApiCompatible,
		labels.MIContainers(0),
		func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			manifest, reader, err := framework.SharedReadOnlyCluster(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to borrow the owner-provisioned node pool")

			By("reading the shared node pool and choosing a different valid OS disk size")
			nodePool, err := reader.GetNodePool20240610(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to read the shared node pool %q", manifest.NodePoolResourceID)
			Expect(nodePool.ID).NotTo(BeNil(), "shared node pool ID was nil")
			Expect(strings.EqualFold(*nodePool.ID, manifest.NodePoolResourceID)).To(BeTrue(), "node pool identity must match the owner-published manifest")
			Expect(nodePool.Properties).NotTo(BeNil(), "shared node pool properties were nil")
			Expect(nodePool.Properties.ProvisioningState).NotTo(BeNil(), "shared node pool provisioning state was nil")
			Expect(*nodePool.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ProvisioningStateSucceeded), "shared node pool must be Succeeded before attempting a rejected update")
			Expect(nodePool.Properties.Platform).NotTo(BeNil(), "shared node pool platform profile was nil")
			Expect(nodePool.Properties.Platform.OSDisk).NotTo(BeNil(), "shared node pool OS disk profile was nil")
			Expect(nodePool.Properties.Platform.OSDisk.SizeGiB).NotTo(BeNil(), "shared node pool OS disk size was nil")
			sizeGiB := int32(128)
			if *nodePool.Properties.Platform.OSDisk.SizeGiB == sizeGiB {
				sizeGiB = 256
			}

			By("serializing the rejected node pool PATCH and verifying the complete node pool remains unchanged")
			_, guarded, err := framework.SharedGuardedNegativeCluster(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to borrow a guarded-negative node pool handle")
			err = guarded.RejectNodePoolPatch20240610(ctx,
				map[string]any{"properties": map[string]any{"platform": map[string]any{"osDisk": map[string]any{"sizeGiB": sizeGiB}}}},
				framework.ClusterPatchRejection{
					StatusCode: http.StatusBadRequest,
					Code:       "MultipleErrorsOccurred",
					Message:    "2 errors occurred",
					Details: []framework.PatchRejectionDetail{
						{Code: "InvalidRequestContent", Target: "properties.platform", Message: "field is immutable"},
						{Code: "InvalidRequestContent", Target: "properties.platform.osDisk", Message: "field is immutable"},
					},
				})
			Expect(err).NotTo(HaveOccurred(), "RP must reject the OS disk size PATCH with typed HTTP 400 / MultipleErrorsOccurred and both immutable-field details, leaving the shared node pool unchanged")
		},
	)
})
