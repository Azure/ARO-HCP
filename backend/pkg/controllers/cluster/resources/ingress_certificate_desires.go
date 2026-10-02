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

package resources

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
)

func buildIngressCertificateDesires(
	key controllerutils.HCPClusterKey,
	managementCluster *fleetapi.ManagementCluster,
	namespace, clusterServiceID, serviceTenantID string,
) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire, error) {
	if serviceTenantID == "" {
		return nil, nil, fmt.Errorf("service tenant ID is required for ingress certificates")
	}
	vaultURL, err := url.Parse(managementCluster.Status.HostedClustersSecretsKeyVaultURL)
	if err != nil || vaultURL.Scheme != "https" || vaultURL.Hostname() == "" {
		return nil, nil, fmt.Errorf("invalid hosted clusters secrets Key Vault URL")
	}
	vaultName, _, _ := strings.Cut(vaultURL.Hostname(), ".")
	secretName := "default-ingress-tls-cert-" + clusterServiceID
	certificateName := "ingress-tls-cert-" + clusterServiceID
	manifests := []struct {
		kind, group, version, resource string
		spec                           map[string]any
	}{
		{
			kind: "SecretProviderClass", group: "secrets-store.csi.x-k8s.io", version: "v1", resource: "secretproviderclasses",
			spec: map[string]any{
				"provider": "azure",
				"parameters": map[string]string{
					"keyvaultName":           vaultName,
					"objects":                fmt.Sprintf("array:\n  - |\n    objectName: %q\n    objectType: secret\n", certificateName),
					"tenantId":               serviceTenantID,
					"usePodIdentity":         "false",
					"useVMManagedIdentity":   "true",
					"userAssignedIdentityID": managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID,
				},
			},
		},
		{
			kind: "SecretSync", group: "secret-sync.x-k8s.io", version: "v1alpha1", resource: "secretsyncs",
			spec: map[string]any{
				"serviceAccountName":      "default",
				"secretProviderClassName": secretName,
				"secretObject": map[string]any{
					"type": "kubernetes.io/tls",
					"data": []map[string]string{
						{"sourcePath": certificateName, "targetKey": "tls.key"},
						{"sourcePath": certificateName, "targetKey": "tls.crt"},
					},
				},
			},
		},
	}
	var applyDesires []*kubeapplierapi.ApplyDesire
	var readDesires []*kubeapplierapi.ReadDesire
	for _, manifest := range manifests {
		content, err := json.Marshal(map[string]any{
			"apiVersion": manifest.group + "/" + manifest.version,
			"kind":       manifest.kind,
			"metadata":   map[string]string{"name": secretName, "namespace": namespace},
			"spec":       manifest.spec,
		})
		if err != nil {
			return nil, nil, err
		}
		desireName := IngressCertificateControllerName + manifest.kind
		applyID, err := azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName))
		if err != nil {
			return nil, nil, err
		}
		readID, err := azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName))
		if err != nil {
			return nil, nil, err
		}
		target := kubeapplierapi.ResourceReference{
			Group: manifest.group, Version: manifest.version, Resource: manifest.resource,
			Namespace: namespace, Name: secretName,
		}
		partitionKey := strings.ToLower(managementCluster.ResourceID.String())
		applyDesires = append(applyDesires, &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: applyID, PartitionKey: partitionKey},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementCluster.ResourceID,
				Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
				TargetItem:        target,
				ServerSideApply:   &kubeapplierapi.ServerSideApplyConfig{KubeContent: &runtime.RawExtension{Raw: content}},
			},
			Tags: map[string]string{kubeapplierapi.TagControllerName: IngressCertificateControllerName},
		})
		readDesires = append(readDesires, &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: readID, PartitionKey: partitionKey},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: managementCluster.ResourceID,
				TargetItem:        target,
			},
			Tags: map[string]string{kubeapplierapi.TagControllerName: IngressCertificateControllerName},
		})
	}
	return applyDesires, readDesires, nil
}
