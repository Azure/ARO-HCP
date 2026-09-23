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

// Desire names used by classifyClusterResource and the applyDesireRemovalChain.
// These names bridge the creation and deletion flows: classifyClusterResource
// assigns them when building ApplyDesires from Cluster Service resources, and
// the removal steps claim them when tearing down a cluster.
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

	DesireNameDefaultIngressConfigMap = "DefaultIngressConfigMap"
	DesireNameOCPPullSecret           = "OCPPullSecret"
	DesireNamePodNetwork              = "PodNetwork"
	DesireNamePodNetworkInstance      = "PodNetworkInstance"

	DesireNameBoundServiceAccountSigningKeySecretSync = "BoundServiceAccountSigningKeySecretSync"
	DesireNameDefaultIngressWildcardCertSecretSync    = "DefaultIngressWildcardCertSecretSync"
	DesireNameKubeAPIServerServingCertSecretSync      = "KubeAPIServerServingCertSecretSync"

	DesireNameBoundServiceAccountSigningKeySecretProviderClass = "BoundServiceAccountSigningKeySecretProviderClass"
	DesireNameDefaultIngressWildcardCertSecretProviderClass    = "DefaultIngressWildcardCertSecretProviderClass"
	DesireNameKubeAPIServerServingCertSecretProviderClass      = "KubeAPIServerServingCertSecretProviderClass"
)
