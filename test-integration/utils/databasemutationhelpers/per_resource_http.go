package databasemutationhelpers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type HTTPTestSpecializer interface {
	Get(ctx context.Context, resourceIDString string) (any, error)
	CreateOrUpdate(ctx context.Context, resourceIDString string, content []byte) error
	Patch(ctx context.Context, resourceIDString string, content []byte) error
	Delete(ctx context.Context, resourceIDString string) error
}

type frontendHTTPTestSpecializer struct {
	dbClient       database.DBClient
	frontendClient *hcpsdk20240610preview.ClientFactory
}

var _ HTTPTestSpecializer = &frontendHTTPTestSpecializer{}

func (c frontendHTTPTestSpecializer) Get(ctx context.Context, resourceIDString string) (any, error) {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	switch strings.ToLower(resourceID.ResourceType.String()) {
	case strings.ToLower(api.ClusterResourceType.String()):
		return c.frontendClient.NewHcpOpenShiftClustersClient().Get(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)

	case strings.ToLower(api.NodePoolResourceType.String()):
		return c.frontendClient.NewNodePoolsClient().Get(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, nil)

	case strings.ToLower(api.ExternalAuthResourceType.String()):
		return c.frontendClient.NewExternalAuthsClient().Get(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, nil)

	default:
		return "", utils.TrackError(fmt.Errorf("unknown resource type: %s", resourceID.ResourceType.String()))
	}
}

func (c frontendHTTPTestSpecializer) CreateOrUpdate(ctx context.Context, resourceIDString string, content []byte) error {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	switch strings.ToLower(resourceID.ResourceType.String()) {
	case strings.ToLower(api.ClusterResourceType.String()):
		obj := hcpsdk20240610preview.HcpOpenShiftCluster{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewHcpOpenShiftClustersClient().BeginCreateOrUpdate(ctx, resourceID.ResourceGroupName, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		if err := integrationutils.MarkOperationsCompleteForName(ctx, c.dbClient, resourceID.SubscriptionID, resourceID.Name); err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.ToLower(api.NodePoolResourceType.String()):
		obj := hcpsdk20240610preview.NodePool{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewNodePoolsClient().BeginCreateOrUpdate(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		if err := integrationutils.MarkOperationsCompleteForName(ctx, c.dbClient, resourceID.SubscriptionID, resourceID.Name); err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.ToLower(api.ExternalAuthResourceType.String()):
		obj := hcpsdk20240610preview.ExternalAuth{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewExternalAuthsClient().BeginCreateOrUpdate(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		if err := integrationutils.MarkOperationsCompleteForName(ctx, c.dbClient, resourceID.SubscriptionID, resourceID.Name); err != nil {
			return utils.TrackError(err)
		}
		return nil

	default:
		return utils.TrackError(fmt.Errorf("unknown resource type: %s", resourceID.ResourceType.String()))
	}
}

func (c frontendHTTPTestSpecializer) Patch(ctx context.Context, resourceIDString string, content []byte) error {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	switch strings.ToLower(resourceID.ResourceType.String()) {
	case strings.ToLower(api.ClusterResourceType.String()):
		obj := hcpsdk20240610preview.HcpOpenShiftClusterUpdate{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewHcpOpenShiftClustersClient().BeginUpdate(ctx, resourceID.ResourceGroupName, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.ToLower(api.NodePoolResourceType.String()):
		obj := hcpsdk20240610preview.NodePoolUpdate{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewNodePoolsClient().BeginUpdate(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.ToLower(api.ExternalAuthResourceType.String()):
		obj := hcpsdk20240610preview.ExternalAuthUpdate{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewExternalAuthsClient().BeginUpdate(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil

	default:
		return utils.TrackError(fmt.Errorf("unknown resource type: %s", resourceID.ResourceType.String()))
	}
}

func (c frontendHTTPTestSpecializer) Delete(ctx context.Context, resourceIDString string) error {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	switch strings.ToLower(resourceID.ResourceType.String()) {
	case strings.ToLower(api.ClusterResourceType.String()):
		_, err := c.frontendClient.NewHcpOpenShiftClustersClient().BeginDelete(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)
		return utils.TrackError(err)

	case strings.ToLower(api.NodePoolResourceType.String()):
		_, err := c.frontendClient.NewNodePoolsClient().BeginDelete(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, nil)
		return utils.TrackError(err)

	case strings.ToLower(api.ExternalAuthResourceType.String()):
		_, err := c.frontendClient.NewExternalAuthsClient().BeginDelete(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, nil)
		return utils.TrackError(err)

	default:
		return utils.TrackError(fmt.Errorf("unknown resource type: %s", resourceID.ResourceType.String()))
	}
}
