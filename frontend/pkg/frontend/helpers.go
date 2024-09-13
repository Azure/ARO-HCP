package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

func (f *Frontend) MarshalResource(ctx context.Context, resourceID *arm.ResourceID, versionedInterface api.Version) ([]byte, *arm.CloudError) {
	var responseBody []byte

	doc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("resource document not found for %s", resourceID))
			return nil, arm.NewResourceNotFoundError(resourceID)
		} else {
			f.logger.Error(fmt.Sprintf("failed to fetch resource document for %s: %v", resourceID, err))
			return nil, arm.NewInternalServerError()
		}
	}

	switch doc.InternalID.Kind() {
	case cmv1.ClusterKind:
		csCluster, err := f.clusterServiceConfig.GetCSCluster(ctx, doc.InternalID)
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
		csNodePool, err := f.clusterServiceConfig.GetCSNodePool(ctx, doc.InternalID)
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
