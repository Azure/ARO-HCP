package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func (a *Admin) AdminClustersListFromCS(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	apiVersion := request.URL.Query().Get(APIVersionKey)
	versionedInterface, _ := api.Lookup(apiVersion)

	/* 	var query string
	   	clusterID := request.URL.Query().Get("id")
	   	clusterName := request.URL.Query().Get("name")

	   	switch {
	   	case clusterName != "":
	   		query = fmt.Sprintf("name='%s'", clusterName)
	   	case clusterID != "":
	   		query = fmt.Sprintf("id='%s'", clusterID)
	   	} */

	pageSize := 10
	pageNumber := 1

	if pageStr := request.URL.Query().Get("page"); pageStr != "" {
		pageNumber, _ = strconv.Atoi(pageStr)
	}
	if sizeStr := request.URL.Query().Get("size"); sizeStr != "" {
		pageSize, _ = strconv.Atoi(sizeStr)
	}

	clustersRequest := a.clusterServiceConfig.Conn.ClustersMgmt().V1().Clusters().List()
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
			cluster.Azure().SubscriptionID(), cluster.Azure().ResourceGroupName(), api.ResourceType,
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
