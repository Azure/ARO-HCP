package admission

import (
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// AdmitNodePool performs non-static checks of nodepool.  Checks that require more information than is contained inside of
// of the nodepool instance itself.
func AdmitNodePool(nodePool *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster) field.ErrorList {
	errs := field.ErrorList{}

	if nodePool.Properties.Version.ChannelGroup != cluster.Properties.Version.ChannelGroup {
		errs = append(errs, field.Invalid(
			field.NewPath("properties", "version", "channelGroup"),
			nodePool.Properties.Version.ChannelGroup,
			fmt.Sprintf("must be the same as control plane channel group '%s'", cluster.Properties.Version.ChannelGroup),
		))
	}

	if len(nodePool.Properties.Platform.SubnetID) > 0 && len(cluster.Properties.Platform.SubnetID) > 0 {
		clusterSubnetResourceID, _ := azcorearm.ParseResourceID(cluster.Properties.Platform.SubnetID)
		nodePoolSubnetResourceID, _ := azcorearm.ParseResourceID(nodePool.Properties.Platform.SubnetID)

		// if this fails, then other validation will fail
		if clusterSubnetResourceID != nil && nodePoolSubnetResourceID != nil {
			clusterVNet := clusterSubnetResourceID.Parent.String()
			nodePoolVNet := nodePoolSubnetResourceID.Parent.String()
			if !strings.EqualFold(nodePoolVNet, clusterVNet) {
				errs = append(errs, field.Invalid(
					field.NewPath("properties", "platform", "subnetId"),
					nodePool.Properties.Version.ChannelGroup,
					fmt.Sprintf("must belong to the same VNet as the parent cluster VNet '%s'", nodePoolSubnetResourceID, clusterVNet),
				))
			}
		}
	}

	return errs
}
