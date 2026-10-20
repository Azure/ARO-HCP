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
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

const ingressSecretProviderClassDesireName = IngressCertificateControllerName + "SecretProviderClass"

func buildIngressCertificateDesires(
	key controllerutils.HCPClusterKey,
	managementCluster *fleetapi.ManagementCluster,
	namespace, serviceTenantID, cloudName string,
	certificate *coreapi.AzureTLSCertificateReference,
) ([]*kubeapplierapi.ApplyDesire, []*kubeapplierapi.ReadDesire, error) {
	return buildCertificateDesires(key, managementCluster, namespace, serviceTenantID, cloudName, certificate,
		"ingress", IngressCertificateControllerName, controllerutils.ServiceProviderDefaultIngressWildcardServingCertName,
		ingressSecretProviderClassDesireName, kubeapplierhelpers.IngressSecretSyncDesireName)
}
