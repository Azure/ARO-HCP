package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func (a *Admin) AdminResourceList(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		a.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var query string
	subscriptionId := request.PathValue(PathSegmentSubscriptionID)
	resourceGroupName := request.PathValue(PathSegmentResourceGroupName)
	location := request.PathValue(PathSegmentLocation)

	switch {
	case resourceGroupName != "":
		query = fmt.Sprintf("azure.resource_group_name='%s'", resourceGroupName)
	case location != "":
		query = fmt.Sprintf("region.id='%s'", location)
	case subscriptionId != "" && location == "" && resourceGroupName == "":
		query = fmt.Sprintf("azure.subscription_id='%s'", subscriptionId)
	}

	pageSize := 10
	pageNumber := 1

	if pageStr := request.URL.Query().Get("page"); pageStr != "" {
		pageNumber, _ = strconv.Atoi(pageStr)
	}
	if sizeStr := request.URL.Query().Get("size"); sizeStr != "" {
		pageSize, _ = strconv.Atoi(sizeStr)
	}

	// Create the request with initial parameters:
	clustersRequest := a.clusterServiceConfig.Conn.ClustersMgmt().V2alpha1().Clusters().List().Search(query)
	clustersRequest.Size(pageSize)
	clustersRequest.Page(pageNumber)

	// Send the initial request:
	clustersListResponse, err := clustersRequest.SendContext(ctx)
	if err != nil {
		a.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var hcpCluster *api.HCPOpenShiftCluster
	var versionedHcpClusters []*api.VersionedHCPOpenShiftCluster
	clusters := clustersListResponse.Items().Slice()
	for _, cluster := range clusters {
		// FIXME Temporary, until we have a real ResourceID to pass.
		azcoreResourceID, err := azcorearm.ParseResourceID(fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/%s/%s",
			subscriptionId, resourceGroupName, api.ResourceType,
			cluster.Azure().ResourceName()))
		if err != nil {
			a.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
		resourceID := &arm.ResourceID{ResourceID: *azcoreResourceID}
		hcpCluster = ConvertCStoHCPOpenShiftCluster(resourceID, cluster)
		versionedResource := versionedInterface.NewHCPOpenShiftCluster(hcpCluster)
		versionedHcpClusters = append(versionedHcpClusters, &versionedResource)
	}

	// Check if there are more pages to fetch and set NextLink if applicable:
	var nextLink string
	if clustersListResponse.Size() >= pageSize {
		nextPage := pageNumber + 1
		nextLink = buildNextLink(request.URL.Path, request.URL.Query(), nextPage, pageSize)
	}

	result := api.VersionedHCPOpenShiftClusterList{
		Value:    versionedHcpClusters,
		NextLink: &nextLink,
	}

	resp, err := json.Marshal(result)
	if err != nil {
		a.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, err = writer.Write(resp)
	if err != nil {
		a.logger.Error(err.Error())
	}
}

func VersionFromContext(ctx context.Context) (api.Version, error) {
	version, ok := ctx.Value(contextKeyVersion).(api.Version)
	if !ok {
		err := &ContextError{
			got: version,
		}
		return version, err
	}
	return version, nil
}

func (c *ContextError) Error() string {
	return fmt.Sprintf(
		"error retrieving value from context, value obtained was '%v' and type obtained was '%T'",
		c.got,
		c.got)
}

type contextKey int
type ContextError struct {
	got any
}

const (
	// Keys for request-scoped data in http.Request contexts
	contextKeyOriginalPath contextKey = iota
	contextKeyBody
	contextKeyLogger
	contextKeyVersion
	contextKeyResourceID
	contextKeyCorrelationData
	contextKeySystemData
	contextKeySubscription
)

func buildNextLink(basePath string, queryParams url.Values, nextPage, pageSize int) string {
	// Clone the existing query parameters
	newParams := make(url.Values)
	for key, values := range queryParams {
		newParams[key] = values
	}

	newParams.Set("page", strconv.Itoa(nextPage))
	newParams.Set("size", strconv.Itoa(pageSize))

	// Construct the next link URL
	nextLink := basePath + "?" + newParams.Encode()
	return nextLink
}
