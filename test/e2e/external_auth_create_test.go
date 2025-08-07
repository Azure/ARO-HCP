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
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Put HCPOpenShiftCluster ExternalAuth", func() {
	var (
		externalAuthsClient *api.ExternalAuthsClient
		customerEnv         *integration.CustomerEnv
	
	)

	BeforeEach(func(ctx context.Context) {
		tc = framework.NewTestContext()

		By("Preparing HCP ExternalAuths client")
		externalAuthsClient = tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient()

		By("Preparing customer environment values")
		customerEnv = &e2eSetup.CustomerEnv
	})

	It("Attempts to create an ExternalAuth for a non-existent HCPOpenShiftCluster",
		labels.RequireHappyPathInfra, labels.Medium, labels.Negative,
		func(ctx context.Context) {
			var (
				externalAuthName = "my-cool-external-auth"
				clusterName      = "non-existing-cluster"
				externalAuth     api.ExternalAuth
				options          *api.ExternalAuthsClientBeginCreateOrUpdateOptions
			)

			By("Sending a PUT request to create ExternalAuth for a non-existent HCPOpenShiftCluster")
			_, err := externalAuthsClient.BeginCreateOrUpdate(ctx, customerEnv.CustomerRGName, clusterName, externalAuthName, externalAuth, options)
			Expect(err).ToNot(BeNil())

			errMessage := "RESPONSE 500: 500 Internal Server Error"
			Expect(err.Error()).To(ContainSubstring(errMessage))
		},
	)
})
