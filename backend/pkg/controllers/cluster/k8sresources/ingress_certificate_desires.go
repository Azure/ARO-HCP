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

package k8sresources

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	secretproviderclassv1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/v1alpha1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
)

const ingressSecretProviderClassDesireName = IngressCertificateControllerName + "SecretProviderClass"

func buildIngressCertificateDesires(
	key controllerutils.HCPClusterKey,
	managementCluster *fleetapi.ManagementCluster,
	namespace, serviceTenantID, cloudName string,
	certificate *coreapi.AzureTLSCertificateReference,
) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire, error) {
	if len(serviceTenantID) == 0 {
		return nil, nil, fmt.Errorf("service tenant ID is required for ingress certificates")
	}
	if certificate == nil {
		return nil, nil, fmt.Errorf("observed ingress certificate is required")
	}
	vaultURL, err := url.Parse(certificate.KeyVaultURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse observed ingress certificate KeyVaultURL: %w", err)
	}
	vaultName, _, _ := strings.Cut(vaultURL.Hostname(), ".")
	if len(certificate.CertificateName) == 0 {
		return nil, nil, fmt.Errorf("observed ingress certificate has no certificate name")
	}
	secretName := controllerutils.ServiceProviderDefaultIngressWildcardServingCertName
	certificateName := certificate.CertificateName
	objectMeta := metav1.ObjectMeta{Name: secretName, Namespace: namespace}

	secretProviderClass := &secretproviderclassv1.SecretProviderClass{
		TypeMeta:   metav1.TypeMeta{Kind: "SecretProviderClass", APIVersion: "secrets-store.csi.x-k8s.io/v1"},
		ObjectMeta: objectMeta,
		Spec: secretproviderclassv1.SecretProviderClassSpec{
			Provider: "azure",
			Parameters: map[string]string{
				"cloudName":    cloudName,
				"keyvaultName": vaultName,
				// Note: by specifying an object whose objectType has the value "secret" we
				// get the full certificate contents: the private key, the public certificate,
				// and the rest of the certificate chain.
				// If the SecretProviderClass is mounted as a csi volume in a K8s Pod then it
				// results in three different mounted files:
				//   - <objectName>: the full certificate data, including the public certificate,
				//     the rest of the certificate chain, and the private key
				//   - <objectName>.crt: the public certificate and the rest of the public
				//     certificate chain
				//   - <objectName>.key: the private key only
				// More information available in https://azure.github.io/secrets-store-csi-driver-provider-azure/docs/configurations/getting-certs-and-keys/#how-to-obtain-the-private-key-and-certificate
				// We deliberately don't define a separate object with objectType "cert"
				// because that results in an object containing the public certificate but
				// not the rest of the full certificate chain. The only way to obtain the full
				// chain is by using objectType "secret" and then only using the
				// <objectName>.crt mounted file.
				"objects": fmt.Sprintf(`array:
  - |
    objectName: %q
    objectType: secret
`, certificateName),
				"tenantId":               serviceTenantID,
				"usePodIdentity":         "false",
				"useVMManagedIdentity":   "true",
				"userAssignedIdentityID": managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID,
			},
		},
	}

	secretSync := &secretsyncv1alpha1.SecretSync{
		TypeMeta:   metav1.TypeMeta{Kind: "SecretSync", APIVersion: "secret-sync.x-k8s.io/v1alpha1"},
		ObjectMeta: objectMeta,
		Spec: secretsyncv1alpha1.SecretSyncSpec{
			ServiceAccountName:      "default",
			SecretProviderClassName: secretName,
			SecretObject: secretsyncv1alpha1.SecretObject{
				Type: "kubernetes.io/tls",
				Data: []secretsyncv1alpha1.SecretObjectData{
					{SourcePath: certificateName, TargetKey: "tls.key"},
					{SourcePath: certificateName, TargetKey: "tls.crt"},
				},
			},
		},
	}

	manifests := []struct {
		group, version, resource, desireName string
		object                               any
	}{
		{
			group: "secrets-store.csi.x-k8s.io", version: "v1", resource: "secretproviderclasses",
			desireName: ingressSecretProviderClassDesireName,
			object:     secretProviderClass,
		},
		{
			group: "secret-sync.x-k8s.io", version: "v1alpha1", resource: "secretsyncs",
			desireName: kubeapplierhelpers.IngressSecretSyncDesireName,
			object:     secretSync,
		},
	}
	var applyDesires []*kubeapplierapi.ApplyDesire
	var readDesires []*kubeapplierapi.ReadDesire
	for _, manifest := range manifests {
		content, err := json.Marshal(manifest.object)
		if err != nil {
			return nil, nil, err
		}
		applyDesireResourceID, err := azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, manifest.desireName))
		if err != nil {
			return nil, nil, err
		}
		readDesireResourceID, err := azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, manifest.desireName))
		if err != nil {
			return nil, nil, err
		}
		target := kubeapplierapi.ResourceReference{
			Group: manifest.group, Version: manifest.version, Resource: manifest.resource,
			Namespace: namespace, Name: secretName,
		}
		partitionKey := strings.ToLower(managementCluster.ResourceID.String())
		applyDesires = append(applyDesires, &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: applyDesireResourceID, PartitionKey: partitionKey},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementCluster.ResourceID,
				Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
				TargetItem:        target,
				ServerSideApply:   &kubeapplierapi.ServerSideApplyConfig{KubeContent: &runtime.RawExtension{Raw: content}},
			},
			Tags: map[string]string{kubeapplierapi.TagControllerName: IngressCertificateControllerName},
		})
		readDesires = append(readDesires, &kubeapplierapi.ReadDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: readDesireResourceID, PartitionKey: partitionKey},
			Spec: kubeapplierapi.ReadDesireSpec{
				ManagementCluster: managementCluster.ResourceID,
				TargetItem:        target,
			},
			Tags: map[string]string{kubeapplierapi.TagControllerName: IngressCertificateControllerName},
		})
	}
	return applyDesires, readDesires, nil
}
