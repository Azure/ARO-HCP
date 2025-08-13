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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("ExternalEntra Step 2: set redirect URI & create Entra group", func() {
	It("reads app creds, gets console route (via breakglass), creates allow group, and patches redirectUris [step2]",
		labels.ExternalAuth, labels.Integration,
		func(ctx SpecContext) {
			// 0) Load Entra app creds
			secretPath := os.Getenv("ENTRA_E2E_SECRET_PATH")
			if secretPath == "" {
				secretPath = "test/e2e/out/entra_app_secret.json"
			}
			b, err := os.ReadFile(secretPath)
			Expect(err).NotTo(HaveOccurred(), "read %s", secretPath)
			var sf providerEntraSecretOut
			Expect(json.Unmarshal(b, &sf)).To(Succeed())
			Expect(sf.AppObjectID).NotTo(BeEmpty())
			Expect(sf.ClientID).NotTo(BeEmpty())

			// 1) Graph token
			token, err := providerGraphToken(ctx)
			Expect(err).NotTo(HaveOccurred())

			// 2) Console route via breakglass
			clusterName := os.Getenv("HCP_CLUSTER_NAME")
			if clusterName == "" {
				clusterName = "external-auth-cluster"
			}
			kc, err := providerFetchBreakglassKubeconfig(ctx, clusterName)
			Expect(err).NotTo(HaveOccurred())

			_ = os.MkdirAll("test/e2e/out", 0o755)
			kcPath := filepath.Join("test", "e2e", "out", "breakglass.kubeconfig")
			Expect(os.WriteFile(kcPath, kc, 0o600)).To(Succeed())
			providerUseKubeconfig(kcPath)

			consoleHost, err := providerGetConsoleRouteHost(ctx)
			Expect(err).NotTo(HaveOccurred())
			By("console route host: " + consoleHost)

			redirect := "https://" + consoleHost + "/oauth/callback"

			// 3) Create allow group
			allowGroupName := os.Getenv("ENTRA_ALLOW_GROUP_NAME")
			if allowGroupName == "" {
				allowGroupName = "aro-e2e-allow-group"
			}
			groupID, err := providerCreateSecurityGroup(ctx, token, allowGroupName)
			Expect(err).NotTo(HaveOccurred())
			By("created Entra allow group: " + groupID)
			Expect(os.WriteFile(filepath.Join("test", "e2e", "out", "entra_group_id.txt"), []byte(groupID), 0o600)).To(Succeed())

			// 4) Patch redirect URIs on the Entra app
			Expect(providerPatchAppRedirectUris(ctx, token, sf.AppObjectID, []string{redirect})).To(Succeed())
			By("redirectUris updated with " + redirect)

			// (Optional) apply ClusterRoleBinding to mgmt cluster using the group
			if strings.TrimSpace(groupID) != "" {
				By("NOTE: apply your ClusterRole/Binding referencing the Entra group if needed.")
			}
		})
})
