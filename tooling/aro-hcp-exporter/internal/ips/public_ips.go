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

package ips

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/internal/utils"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
)

//[{"ipTagType":"FirstPartyUsage","tag":"/Unprivileged"}]

type IPTag struct {
	ServiceTagType  string `json:"ipTagType"`
	ServiceTagValue string `json:"tag"`
}

type PublicIPAddress struct {
	Location       string
	ServiceTags    []IPTag
	SubscriptionId string
	Count          float64
}

// GetAllPublicIPAddresses retrieves all public IP addresses from Azure using the provided client.
// It handles paging automatically and returns all public IP addresses across all resource groups
// in the subscription associated with the client.
func GetAllPublicIPAddresses(ctx context.Context, client *armnetwork.PublicIPAddressesClient, region string) ([]PublicIPAddress, error) {
	var allIPs []PublicIPAddress

	return allIPs, nil
}

// DiscoverClusters discovers AKS clusters using Azure Resource Graph
func DiscoverPublicIPAddresses(ctx context.Context, client *armresourcegraph.Client) ([]PublicIPAddress, error) {
	kqlQuery := buildKQLQuery()
	queryRequest := &armresourcegraph.QueryRequest{
		Query:         &kqlQuery,
		Subscriptions: []*string{to.Ptr("5299e6b7-b23b-46c8-8277-dc1147807117")},
	}
	result, err := client.Resources(ctx, *queryRequest, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Resource Graph query: %w", err)
	}
	return parseResourceGraphResults(result)

}

func buildKQLQuery() string {
	var query strings.Builder

	query.WriteString("resources\n")
	query.WriteString("| where type == 'microsoft.network/publicipaddresses'\n")
	query.WriteString("| summarize Count=count()  by  subscriptionId, location, tostring(properties['ipTags'])\n")

	return query.String()
}

func parseResourceGraphResults(result armresourcegraph.ClientResourcesResponse) ([]PublicIPAddress, error) {
	publicIPAddresses := []PublicIPAddress{}
	rows, err := utils.ParseResourceGraphResultData(result.Data)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		fmt.Println(row)
		ipTags, err := parseIPTags(utils.ParseStringField(row, "ipTags"))
		if err != nil {
			return nil, err
		}
		count := utils.ParseIntField(row, "Count")
		if count == nil {
			continue
		}
		publicIPAddress := PublicIPAddress{
			SubscriptionId: utils.ParseStringField(row, "subscriptionId"),
			Location:       utils.ParseStringField(row, "location"),
			ServiceTags:    ipTags,
			Count:          *count,
		}
		publicIPAddresses = append(publicIPAddresses, publicIPAddress)
	}
	fmt.Println(publicIPAddresses)
	return publicIPAddresses, nil
}

func parseIPTags(ipTagsAsString string) ([]IPTag, error) {
	ipTags := []IPTag{}

	if ipTagsAsString == "" {
		return ipTags, nil
	}
	err := json.Unmarshal([]byte(ipTagsAsString), &ipTags)
	if err != nil {
		return nil, fmt.Errorf("error parsing IPs %s, %w", ipTagsAsString, err)
	}
	return ipTags, nil
}
