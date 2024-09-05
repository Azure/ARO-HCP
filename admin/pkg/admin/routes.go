package admin

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
)

const (
	WildcardActionName        = "{" + PathSegmentActionName + "}"
	WildcardDeploymentName    = "{" + PathSegmentDeploymentName + "}"
	WildcardLocation          = "{" + PathSegmentLocation + "}"
	WildcardNodePoolName      = "{" + PathSegmentNodePoolName + "}"
	WildcardResourceGroupName = "{" + PathSegmentResourceGroupName + "}"
	WildcardResourceName      = "{" + PathSegmentResourceName + "}"
	WildcardSubscriptionID    = "{" + PathSegmentSubscriptionID + "}"

	PatternSubscriptions  = "subscriptions/" + WildcardSubscriptionID
	PatternLocations      = "locations/" + WildcardLocation
	PatternProviders      = "providers/" + api.ProviderNamespace
	PatternClusters       = api.ClusterResourceTypeName + "/" + WildcardResourceName
	PatternNodePools      = api.NodePoolResourceTypeName + "/" + WildcardNodePoolName
	PatternDeployments    = "deployments/" + WildcardDeploymentName
	PatternResourceGroups = "resourcegroups/" + WildcardResourceGroupName
)

func (a *Admin) adminRoutes() *http.ServeMux {

	adminMux := http.NewServeMux()

	adminMux.HandleFunc(MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, api.ClusterResourceTypeName), a.AdminResourceList)

	return adminMux
}

func MuxPattern(method string, segments ...string) string {
	return fmt.Sprintf("%s /%s", method, strings.ToLower(path.Join(segments...)))
}
