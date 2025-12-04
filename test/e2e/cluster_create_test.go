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

package e2e

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Put HCPOpenShiftCluster", func() {
	It("Attempts to put HCPOpenshiftCluster with non-existent Resource Group and cluster resource as nil",
		labels.CreateCluster,
		labels.RequireNothing,
		labels.Medium,
		labels.Negative, FlakeAttempts(3),
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			clusterName := "non-existing-cluster"
			customerRGName := "non-existing-group"
			var (
				clusterResource hcpsdk20240610preview.HcpOpenShiftCluster
				clusterOptions  *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions
			)

			By("Sending put request to create HCPOpenshiftCluster")
			_, err := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().BeginCreateOrUpdate(ctx, customerRGName, clusterName, clusterResource, clusterOptions)
			Expect(err).ToNot(BeNil())
			errMessage := "The location property is required"
			Expect(strings.ToLower(err.Error())).To(ContainSubstring(strings.ToLower(errMessage)))
		})
})
