package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// CheckForProvisioningStateConflict returns a "409 Conflict" error response if the
// provisioning state of the resource is non-terminal, or any of its parent resources
// within the same provider namespace are in a "Deleting" state.
func (f *Frontend) CheckForProvisioningStateConflict(ctx context.Context, operationRequest database.OperationRequest, doc *database.ResourceDocument) *arm.CloudError {
	switch operationRequest {
	case database.OperationRequestCreate:
		// Resource must already exist for there to be a conflict.
	case database.OperationRequestDelete:
		if doc.ProvisioningState == arm.ProvisioningStateDeleting {
			return arm.NewCloudError(
				http.StatusConflict,
				arm.CloudErrorCodeConflict,
				doc.Key.String(),
				"Resource is already deleting")
		}
	case database.OperationRequestUpdate:
		if !doc.ProvisioningState.IsTerminal() {
			return arm.NewCloudError(
				http.StatusConflict,
				arm.CloudErrorCodeConflict,
				doc.Key.String(),
				"Cannot update resource while resource is %s",
				strings.ToLower(string(doc.ProvisioningState)))
		}
	}

	parent := doc.Key.GetParent()

	// ResourceType casing is preserved for parents in the same namespace.
	for parent.ResourceType.Namespace == doc.Key.ResourceType.Namespace {
		parentDoc, err := f.dbClient.GetResourceDoc(ctx, parent)
		if err != nil {
			f.logger.Error(err.Error())
			return arm.NewInternalServerError()
		}

		if parentDoc.ProvisioningState == arm.ProvisioningStateDeleting {
			return arm.NewCloudError(
				http.StatusConflict,
				arm.CloudErrorCodeConflict,
				doc.Key.String(),
				"Cannot %s resource while parent resource is deleting",
				strings.ToLower(string(operationRequest)))
		}

		parent = parent.GetParent()
	}

	return nil
}

func (f *Frontend) DeleteAllResources(ctx context.Context, subscriptionID string) *arm.CloudError {
	prefix, err := arm.ParseResourceID("/subscriptions/" + subscriptionID)
	if err != nil {
		f.logger.Error(err.Error())
		return arm.NewInternalServerError()
	}

	dbIterator := f.dbClient.ListResourceDocs(ctx, prefix, -1, nil)

	// Start a deletion operation for all clusters under the subscription.
	// Cluster Service will delete all node pools belonging to these clusters
	// so we don't need to explicitly delete node pools here.
	for item := range dbIterator.Items(ctx) {
		var resourceDoc *database.ResourceDocument

		err = json.Unmarshal(item, &resourceDoc)
		if err != nil {
			f.logger.Error(err.Error())
			return arm.NewInternalServerError()
		}

		if !strings.EqualFold(resourceDoc.Key.ResourceType.String(), api.ClusterResourceType.String()) {
			continue
		}

		// Allow this method to be idempotent.
		if resourceDoc.ProvisioningState != arm.ProvisioningStateDeleting {
			_, cloudError := f.DeleteResource(ctx, resourceDoc)
			if cloudError != nil {
				return cloudError
			}
		}
	}

	return nil
}

func (f *Frontend) DeleteResource(ctx context.Context, resourceDoc *database.ResourceDocument) (string, *arm.CloudError) {
	const operationRequest = database.OperationRequestDelete
	var err error

	switch resourceDoc.InternalID.Kind() {
	case cmv1.ClusterKind:
		err = f.clusterServiceClient.DeleteCSCluster(ctx, resourceDoc.InternalID)

	case cmv1.NodePoolKind:
		err = f.clusterServiceClient.DeleteCSNodePool(ctx, resourceDoc.InternalID)

	default:
		f.logger.Error(fmt.Sprintf("unsupported Cluster Service path: %s", resourceDoc.InternalID))
		return "", arm.NewInternalServerError()
	}

	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
			return "", arm.NewResourceNotFoundError(resourceDoc.Key)
		}
		f.logger.Error(err.Error())
		return "", arm.NewInternalServerError()
	}

	// Cluster Service will take care of canceling any ongoing operations
	// on the resource or child resources, but we need to do some database
	// bookkeeping to reflect that.

	// FIXME This would be a good place to use Cosmos DB's transactional batch
	//       operations to ensure all these write operations succeed together
	//       or roll back. We would need two parallel transactions: one for
	//       the Operations container and another for the Resources container.
	//       But we're stymied currently by the DBClient interface, and I have
	//       no desire to implement this in the in-memory cache. DBClient has
	//       served us well up to this point, but I think it's time to bid it
	//       farewell and switch to gomock in unit tests.

	err = f.CancelActiveOperation(ctx, resourceDoc)
	if err != nil {
		f.logger.Error(err.Error())
		return "", arm.NewInternalServerError()
	}

	operationDoc := database.NewOperationDocument(operationRequest, resourceDoc.Key, resourceDoc.InternalID)

	err = f.dbClient.CreateOperationDoc(ctx, operationDoc)
	if err != nil {
		f.logger.Error(err.Error())
		return "", arm.NewInternalServerError()
	}

	_, err = f.dbClient.UpdateResourceDoc(ctx, resourceDoc.Key, func(updateDoc *database.ResourceDocument) bool {
		updateDoc.ActiveOperationID = operationDoc.ID
		updateDoc.ProvisioningState = operationDoc.Status
		return true
	})
	if err != nil {
		f.logger.Error(err.Error())
		return "", arm.NewInternalServerError()
	}

	iterator := f.dbClient.ListResourceDocs(ctx, resourceDoc.Key, -1, nil)

	for item := range iterator.Items(ctx) {
		// Anonymous function avoids repetitive error handling.
		err = func() error {
			var child database.ResourceDocument

			err = json.Unmarshal(item, &child)
			if err != nil {
				return err
			}

			err = f.CancelActiveOperation(ctx, &child)
			if err != nil {
				return err
			}

			// This operation is not accessible through any REST endpoint.
			// Its purpose is to cause the backend to delete the resource
			// document once resource deletion completes.

			childOperationDoc := database.NewOperationDocument(operationRequest, child.Key, child.InternalID)

			err = f.dbClient.CreateOperationDoc(ctx, childOperationDoc)
			if err != nil {
				return err
			}

			_, err = f.dbClient.UpdateResourceDoc(ctx, child.Key, func(updateDoc *database.ResourceDocument) bool {
				updateDoc.ActiveOperationID = childOperationDoc.ID
				updateDoc.ProvisioningState = childOperationDoc.Status
				return true
			})
			if err != nil {
				return err
			}

			return nil
		}()
		if err != nil {
			f.logger.Error(err.Error())
			return "", arm.NewInternalServerError()
		}
	}

	err = iterator.GetError()
	if err != nil {
		f.logger.Error(err.Error())
		return "", arm.NewInternalServerError()
	}

	return operationDoc.ID, nil
}

func (f *Frontend) MarshalResource(ctx context.Context, resourceID *arm.ResourceID, versionedInterface api.Version) ([]byte, *arm.CloudError) {
	var responseBody []byte

	doc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		f.logger.Error(err.Error())
		if errors.Is(err, database.ErrNotFound) {
			return nil, arm.NewResourceNotFoundError(resourceID)
		} else {
			return nil, arm.NewInternalServerError()
		}
	}

	switch doc.InternalID.Kind() {
	case cmv1.ClusterKind:
		csCluster, err := f.clusterServiceClient.GetCSCluster(ctx, doc.InternalID)
		if err != nil {
			f.logger.Error(err.Error())
			var ocmError *ocmerrors.Error
			if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
				return nil, arm.NewResourceNotFoundError(resourceID)
			}
			return nil, arm.NewInternalServerError()
		}
		responseBody, err = marshalCSCluster(csCluster, doc, versionedInterface)
		if err != nil {
			f.logger.Error(err.Error())
			return nil, arm.NewInternalServerError()
		}

	case cmv1.NodePoolKind:
		csNodePool, err := f.clusterServiceClient.GetCSNodePool(ctx, doc.InternalID)
		if err != nil {
			f.logger.Error(err.Error())
			var ocmError *ocmerrors.Error
			if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
				return nil, arm.NewResourceNotFoundError(resourceID)
			}
			return nil, arm.NewInternalServerError()
		}
		responseBody, err = marshalCSNodePool(csNodePool, doc, versionedInterface)
		if err != nil {
			f.logger.Error(err.Error())
			return nil, arm.NewInternalServerError()
		}

	default:
		f.logger.Error(fmt.Sprintf("unsupported Cluster Service path: %s", doc.InternalID))
		return nil, arm.NewInternalServerError()
	}

	return responseBody, nil
}
