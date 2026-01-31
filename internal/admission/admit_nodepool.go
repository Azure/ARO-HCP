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

package admission

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
)

// AdmitNodePool performs non-static checks of nodepool.  Checks that require more information than is contained inside of
// of the nodepool instance itself.
func AdmitNodePool(nodePool *api.NodePool, cluster *api.Cluster) field.ErrorList {
	errs := field.ErrorList{}

	if nodePool.Properties.Version.ChannelGroup != cluster.CustomerProperties.Version.ChannelGroup {
		errs = append(errs, field.Invalid(
			field.NewPath("properties", "version", "channelGroup"),
			nodePool.Properties.Version.ChannelGroup,
			fmt.Sprintf("must be the same as control plane channel group '%s'", cluster.CustomerProperties.Version.ChannelGroup),
		))
	}

	if nodePool.Properties.Platform.SubnetID != nil && cluster.CustomerProperties.Platform.SubnetID != nil {
		clusterVNet := cluster.CustomerProperties.Platform.SubnetID.Parent.String()
		nodePoolVNet := nodePool.Properties.Platform.SubnetID.Parent.String()
		if !strings.EqualFold(nodePoolVNet, clusterVNet) {
			errs = append(errs, field.Invalid(
				field.NewPath("properties", "platform", "subnetId"),
				nodePool.Properties.Platform.SubnetID,
				fmt.Sprintf("must belong to the same VNet as the parent cluster VNet '%s'", clusterVNet),
			))
		}
	}

	return errs
}
