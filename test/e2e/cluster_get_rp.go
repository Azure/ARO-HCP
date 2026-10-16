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
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	It("should get an existing HCP cluster and report a missing cluster through the RP",
		labels.RequireSharedReadOnlyCluster,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(0),
		func(ctx context.Context) {
			manifest, client, err := framework.SharedReadOnlyCluster(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to borrow the run-owned read-only cluster")

			By("getting the existing shared cluster through the RP")
			cluster, err := client.Get20240610(ctx, manifest.ClusterName)
			Expect(err).NotTo(HaveOccurred(), "failed to GET shared cluster %q", manifest.ClusterResourceID)
			Expect(cluster.ID).NotTo(BeNil(), "cluster ID was nil")
			Expect(strings.EqualFold(*cluster.ID, manifest.ClusterResourceID)).To(BeTrue(), "cluster ID must match the fixture resource ID")
			Expect(cluster.Name).NotTo(BeNil(), "cluster name was nil")
			Expect(*cluster.Name).To(Equal(manifest.ClusterName), "cluster name must match the fixture")
			Expect(cluster.Location).NotTo(BeNil(), "cluster location was nil")
			Expect(*cluster.Location).To(Equal(manifest.Location), "cluster location must match the fixture")
			Expect(cluster.Properties).NotTo(BeNil(), "cluster properties were nil")
			Expect(cluster.Properties.Platform).NotTo(BeNil(), "platform profile was nil")
			Expect(cluster.Properties.Platform.ManagedResourceGroup).NotTo(BeNil(), "managed resource group was nil")
			Expect(*cluster.Properties.Platform.ManagedResourceGroup).To(Equal(manifest.ManagedResourceGroup), "managed RG must match the fixture")
			Expect(cluster.Properties.ProvisioningState).NotTo(BeNil(), "provisioning state was nil")
			Expect(*cluster.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ProvisioningStateSucceeded), "shared cluster must remain Succeeded")

			By("getting a nonexistent cluster in the same existing customer resource group")
			missingName := "missing-" + rand.String(12)
			_, err = client.Get20240610(ctx, missingName)
			Expect(err).To(HaveOccurred(), "GET of missing cluster %q must fail", missingName)
			var responseError *azcore.ResponseError
			Expect(errors.As(err, &responseError)).To(BeTrue(), "missing cluster GET must return a typed Azure response error: %v", err)
			Expect(framework.IsNotFoundError(err)).To(BeTrue(), "missing cluster GET must return HTTP 404: %v", err)
			Expect(responseError.ErrorCode).To(Equal("ResourceNotFound"), "missing cluster GET must return ResourceNotFound")
		},
	)
})
