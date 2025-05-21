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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

type contextKey string

const systemDataKey = contextKey("systemData")

func prepareSystemData() arm.SystemData {
	createdBy := "E2E Testing"
	createdByType := arm.CreatedByType(arm.CreatedByTypeApplication)
	createdAt := time.Now().UTC()
	return arm.SystemData{
		CreatedBy:     createdBy,
		CreatedByType: createdByType,
		CreatedAt:     &createdAt,
	}
}

var _ = Describe("Cluster put operations", func() {
	var (
		clustersClient *api.HcpOpenShiftClustersClient
		customerEnv    *integration.CustomerEnv
		cluster        *integration.Cluster
	)

	BeforeEach(func() {
		By("Prepare HCP clusters client")
		clustersClient = clients.NewHcpOpenShiftClustersClient()
		By("Prepare customer environment")
		customerEnv = &e2eSetup.CustomerEnv
		By("Prepare e2esetup cluster resource")
		cluster = &e2eSetup.Cluster
	})

	Context("Negative", func() {
		It("Try to create cluster with managed identities and location", labels.Low, labels.Negative, func(ctx context.Context) {
			By("Preparing location and systemData")
			ctxWithSystemData := context.WithValue(ctx, systemDataKey, prepareSystemData())
			cluster.ARMData.Location = &location
			By("Sending Put Request")
			poller, err := clustersClient.BeginCreateOrUpdate(ctxWithSystemData, customerEnv.CustomerRGName, cluster.Name, cluster.ARMData, nil)
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("500 Internal Server Error"))
			Expect(poller).To(BeNil())
		})
	})

})
