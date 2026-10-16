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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Immutable cluster configuration", func() {
	It("rejects an immutable cluster DNS prefix PATCH without changing the shared cluster",
		labels.RequireSharedReadOnlyCluster,
		labels.High,
		labels.Negative,
		labels.AroRpApiCompatible,
		labels.MIContainers(0),
		func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			manifest, reader, err := framework.SharedReadOnlyCluster(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to borrow the run-owned cluster for immutable-update validation")

			By("choosing a different, syntactically valid DNS prefix")
			cluster, err := reader.Get20240610(ctx, manifest.ClusterName)
			Expect(err).NotTo(HaveOccurred(), "failed to read the fixture cluster's current DNS prefix")
			Expect(cluster.Properties).NotTo(BeNil(), "fixture cluster properties were nil")
			Expect(cluster.Properties.DNS).NotTo(BeNil(), "fixture cluster DNS profile was nil")
			Expect(cluster.Properties.DNS.BaseDomainPrefix).NotTo(BeNil(), "fixture cluster DNS prefix was nil")
			prefix := "immutable-a"
			if *cluster.Properties.DNS.BaseDomainPrefix == prefix {
				prefix = "immutable-b"
			}

			By("serializing the rejected PATCH and verifying the entire cluster GET remains unchanged")
			_, guarded, err := framework.SharedGuardedNegativeCluster(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to acquire a guarded-negative fixture handle")
			err = guarded.RejectPatch20240610(ctx,
				map[string]any{"properties": map[string]any{"dns": map[string]any{"baseDomainPrefix": prefix}}},
				framework.ClusterPatchRejection{
					StatusCode: http.StatusBadRequest,
					Code:       "InvalidRequestContent",
					Target:     "properties.dns.baseDomainPrefix",
					Message:    "field is immutable",
				})
			Expect(err).NotTo(HaveOccurred(), "RP must reject the immutable DNS prefix with typed HTTP 400 / InvalidRequestContent and leave the shared cluster unchanged")
		},
	)
})
