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

package clusterresources

import (
	"strings"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/k8sresources"
)

// Desire names used by classifyClusterResource and the applyDesireRemovalChain.
// These names bridge the creation and deletion flows: classifyClusterResource
// assigns them when building ApplyDesires from Cluster Service resources, and
// the removal steps claim them when tearing down a cluster.
//
// Kube-apiserver certificate names are recognized only for handoff to their
// dedicated controller; ClusterResources neither creates nor removes them.
//
// This enumeration is closed: a new resource type cannot reach Cosmos without
// being added to classifyClusterResource first, and adding it there without
// assigning it to a removal step trips the unclaimed-desire warning in
// deleteAllOwnedApplyDesires.
const (
	DesireNameHostedCluster = "HostedCluster"
	DesireNameNodePool      = "NodePool"

	DesireNameManagedCluster = "ManagedCluster"

	DesireNameHostedClusterNamespace = "HostedClusterNamespace"
	DesireNameControlPlaneNamespace  = "ControlPlaneNamespace"

	DesireNameDefaultIngressConfigMap  = "DefaultIngressConfigMap"
	DesireNameExternalAuthCAConfigMap  = "ExternalAuthCAConfigMap"
	DesireNameOCPPullSecret            = "OCPPullSecret"
	DesireNameExternalAuthClientSecret = "ExternalAuthClientSecret"
	DesireNamePodNetwork               = "PodNetwork"
	DesireNamePodNetworkInstance       = "PodNetworkInstance"

	DesireNameBoundServiceAccountSigningKeySecretSync = "BoundServiceAccountSigningKeySecretSync"
	DesireNameDefaultIngressWildcardCertSecretSync    = "DefaultIngressWildcardCertSecretSync"
	DesireNameKubeAPIServerServingCertSecretSync      = k8sresources.KubeAPIServerServingCertSecretSyncDesireName

	DesireNameBoundServiceAccountSigningKeySecretProviderClass = "BoundServiceAccountSigningKeySecretProviderClass"
	DesireNameDefaultIngressWildcardCertSecretProviderClass    = "DefaultIngressWildcardCertSecretProviderClass"
	DesireNameKubeAPIServerServingCertSecretProviderClass      = k8sresources.KubeAPIServerServingCertSecretProviderClassDesireName
)

// managedByKubeAPIServerServingCertificate also recognizes desires that still
// carry the old ClusterResources owner tag, before the new controller adopts them.
func managedByKubeAPIServerServingCertificate(name string) bool {
	return strings.EqualFold(name, DesireNameKubeAPIServerServingCertSecretSync) ||
		strings.EqualFold(name, DesireNameKubeAPIServerServingCertSecretProviderClass)
}
