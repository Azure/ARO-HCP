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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("External auth issuer validation", func() {
	It("rejects an invalid external auth issuer URL without leaving a child or changing the shared cluster",
		labels.RequireSharedReadOnlyCluster,
		labels.High,
		labels.Negative,
		labels.AroRpApiCompatible,
		labels.MIContainers(0),
		func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			_, guarded, err := framework.SharedGuardedNegativeCluster(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to borrow the run-owned cluster for external auth validation")

			By("serializing an invalid issuer PUT to a unique child and verifying typed rejection, child absence, and unchanged parent state")
			err = guarded.RejectExternalAuthIssuer20240610(ctx, "invalid-"+rand.String(7), "invalid-url")
			Expect(err).NotTo(HaveOccurred(), "RP must reject properties.issuer.url with typed HTTP 400 / InvalidRequestContent, leave no external auth child, and preserve the shared cluster")
		},
	)
})
