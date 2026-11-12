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
	ocmv1 "open-cluster-management.io/api/work/v1"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

// Retain the existing desire names so ownership transfers in place from ClusterResources.
const (
	KubeAPIServerServingCertSecretProviderClassDesireName = "KubeAPIServerServingCertSecretProviderClass"
	KubeAPIServerServingCertSecretSyncDesireName          = "KubeAPIServerServingCertSecretSync"
	kubeAPIServerServingCertSecretName                    = "kube-apiserver-tls-cert"
)

func buildKubeAPIServerServingCertificateDesires(
	key controllerutils.HCPClusterKey,
	managementCluster *fleetapi.ManagementCluster,
	namespace, serviceTenantID, cloudName string,
	certificate *coreapi.AzureTLSCertificateReference,
) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire, error) {
	applies, reads, err := buildCertificateDesires(key, managementCluster, namespace, serviceTenantID, cloudName, certificate,
		"kube-apiserver serving", KubeAPIServerServingCertificateControllerName, kubeAPIServerServingCertSecretName,
		KubeAPIServerServingCertSecretProviderClassDesireName, KubeAPIServerServingCertSecretSyncDesireName)
	if err != nil {
		return nil, nil, err
	}
	for _, desire := range applies {
		// These are the same objects still applied by Cluster Service's work-agent.
		// Keep its field manager until ACM and Maestro are removed.
		desire.Spec.ServerSideApply.FieldManager = ptr.To(ocmv1.DefaultFieldManager)
	}
	return applies, reads, nil
}
