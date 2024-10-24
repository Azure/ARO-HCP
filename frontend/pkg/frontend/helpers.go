package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
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

func (f *Frontend) DeleteResource(ctx context.Context, resourceID *arm.ResourceID) (*database.ResourceDocument, *arm.CloudError) {
	doc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, arm.NewResourceNotFoundError(resourceID)
		} else {
			f.logger.Error(err.Error())
			return nil, arm.NewInternalServerError()
		}
	}

	switch doc.InternalID.Kind() {
	case cmv1.ClusterKind:
		err = f.clusterServiceClient.DeleteCSCluster(ctx, doc.InternalID)

	case cmv1.NodePoolKind:
		err = f.clusterServiceClient.DeleteCSNodePool(ctx, doc.InternalID)

	default:
		f.logger.Error(fmt.Sprintf("unsupported Cluster Service path: %s", doc.InternalID))
		return nil, arm.NewInternalServerError()
	}

	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
			return nil, arm.NewResourceNotFoundError(resourceID)
		}
		f.logger.Error(err.Error())
		return nil, arm.NewInternalServerError()
	}

	return doc, nil
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
