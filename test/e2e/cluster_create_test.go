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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Put HCPOpenShiftCluster", func() {
	It("Attempts to put HCPOpenshiftCluster with non-existant Resource Group", labels.RequireNothing, labels.Medium, labels.Negative, func(ctx context.Context) {
		tc := framework.NewTestContext()

		clusterName := "non-existing-cluster"
		customerRGName := "non-existing-group"
		var (
			clusterResource api.HcpOpenShiftCluster
			clusterOptions  *api.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions
		)

		By("Sending put request to create HCPOpenshiftCluster")
		_, err := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().BeginCreateOrUpdate(ctx, customerRGName, clusterName, clusterResource, clusterOptions)
		Expect(err).ToNot(BeNil())
		errMessage := "RESPONSE 500: 500 Internal Server Error"
		Expect(err.Error()).To(ContainSubstring(errMessage))
	})
})
