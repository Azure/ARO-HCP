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

package controllers

import "fmt"

// HostedClusterNamespace returns the management-cluster namespace that
// hosts a given HCP's HostedCluster / NodePool objects. Cluster Service
// names it "ocm-<envIdentifier>-<csClusterID>" and we must mirror that
// exactly so the kube-applier targets the right namespace.
func HostedClusterNamespace(envIdentifier, csClusterID string) string {
	return fmt.Sprintf("ocm-%s-%s", envIdentifier, csClusterID)
}
