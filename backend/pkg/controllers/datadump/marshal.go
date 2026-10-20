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

package datadump

import (
	"bytes"
	"fmt"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

// MarshalClusterServiceAny serializes SDK types for integration-test fixtures.
// Cluster Service types do not implement the standard JSON interfaces.
func MarshalClusterServiceAny(clusterServiceData any) ([]byte, error) {
	buf := &bytes.Buffer{}
	var err error
	switch obj := clusterServiceData.(type) {
	case *arohcpv1alpha1.Cluster:
		err = arohcpv1alpha1.MarshalCluster(obj, buf)
	case *arohcpv1alpha1.ClusterAutoscaler:
		err = arohcpv1alpha1.MarshalClusterAutoscaler(obj, buf)
	case *arohcpv1alpha1.ExternalAuth:
		err = arohcpv1alpha1.MarshalExternalAuth(obj, buf)
	case *arohcpv1alpha1.NodePool:
		err = arohcpv1alpha1.MarshalNodePool(obj, buf)
	case *arohcpv1alpha1.ProvisionShard:
		err = arohcpv1alpha1.MarshalProvisionShard(obj, buf)
	case *cmv1.HypershiftConfig:
		err = cmv1.MarshalHypershiftConfig(obj, buf)
	default:
		return nil, fmt.Errorf("unknown type: %T", obj)
	}
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
