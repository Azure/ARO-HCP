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

package controllerutils

import (
	"fmt"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

// HostedClusterNamespace returns the management-cluster namespace that
// hosts a given HCP's HostedCluster / NodePool objects. Cluster Service
// names it "ocm-<envIdentifier>-<csClusterID>" and we must mirror that
// exactly so the kube-applier targets the right namespace.
func HostedClusterNamespace(envIdentifier, csClusterID string) string {
	return fmt.Sprintf("ocm-%s-%s", envIdentifier, csClusterID)
}

// HostedControlPlaneNamespace returns the management-cluster namespace that hosts a
// given HCP's control plane workloads (including ControlPlaneComponent CRs).
// Hypershift names it "ocm-<envIdentifier>-<csClusterID>-<csClusterDomainPrefix>".
func HostedControlPlaneNamespace(envIdentifier, csClusterID, csClusterDomainPrefix string) string {
	return fmt.Sprintf("ocm-%s-%s-%s", envIdentifier, csClusterID, csClusterDomainPrefix)
}

// HostedClusterTarget builds the ResourceReference that points at a
// cluster's HostedCluster object in the management cluster. The naming
// rules (namespace = "ocm-<env>-<csClusterID>", name = csClusterDomainPrefix)
// match what CS itself uses. This is the single, shared derivation of the
// HostedCluster's identity: both the read path (mirroring the HostedCluster
// for observability) and any apply path (writing individual fields onto it)
// must use this exact same function so they can never disagree about which
// object they mean.
func HostedClusterTarget(envIdentifier, csClusterID, csClusterDomainPrefix string) kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group:     hsv1beta1.SchemeGroupVersion.Group,
		Version:   hsv1beta1.SchemeGroupVersion.Version,
		Resource:  "hostedclusters",
		Namespace: HostedClusterNamespace(envIdentifier, csClusterID),
		Name:      csClusterDomainPrefix,
	}
}
