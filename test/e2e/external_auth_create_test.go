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

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Put HCPOpenShiftCluster ExternalAuth", func() {
	var (
		ExternalAuthsClient *api.ExternalAuthsClient
		customerEnv         *integration.CustomerEnv
	)

	BeforeEach(func() {
		By("Preparing HCP externalAuths client")
		ExternalAuthsClient = clients.NewExternalAuthsClient()
		By("Preparing customer environment values")
		customerEnv = &e2eSetup.CustomerEnv
	})

	It("Attempts to create a external auth for a non-existant HCPOpenshiftCluster", labels.RequireHappyPathInfra, labels.Medium, labels.Negative, func(ctx context.Context) {
		var (
			nodePoolName     = "my-cool-external-auth"
			clusterName      = "non-existing_cluster"
			nodePoolResource api.ExternalAuth
			nodePoolOptions  *api.ExternalAuthsClientBeginCreateOrUpdateOptions
		)

		By("Sending a  put request to create external auth for non-existing HCPOpenshiftCluster")
		_, err := ExternalAuthsClient.BeginCreateOrUpdate(ctx, customerEnv.CustomerRGName, clusterName, nodePoolName, nodePoolResource, nodePoolOptions)
		Expect(err).ToNot(BeNil())
		errMessage := "RESPONSE 500: 500 Internal Server Error"
		Expect(err.Error()).To(ContainSubstring(errMessage))
	})
})
