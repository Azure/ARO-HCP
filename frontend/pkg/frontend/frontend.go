package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/google/uuid"
	cmv2alpha1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v2alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type Frontend struct {
	clusterServiceConfig ocm.ClusterServiceConfig
	logger               *slog.Logger
	listener             net.Listener
	server               http.Server
	dbClient             database.DBClient
	ready                atomic.Value
	done                 chan struct{}
	metrics              Emitter
	location             string
}

func NewFrontend(logger *slog.Logger, listener net.Listener, emitter Emitter, dbClient database.DBClient, location string, csCfg ocm.ClusterServiceConfig) *Frontend {
	f := &Frontend{
		clusterServiceConfig: csCfg,
		logger:               logger,
		listener:             listener,
		metrics:              emitter,
		server: http.Server{
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
			BaseContext: func(net.Listener) context.Context {
				return ContextWithLogger(context.Background(), logger)
			},
		},
		dbClient: dbClient,
		done:     make(chan struct{}),
		location: location,
	}

	f.server.Handler = f.routes()

	return f
}

func (f *Frontend) Run(ctx context.Context, stop <-chan struct{}) {
	if stop != nil {
		go func() {
			<-stop
			f.ready.Store(false)
			_ = f.server.Shutdown(ctx)
		}()
	}

	f.logger.Info(fmt.Sprintf("listening on %s", f.listener.Addr().String()))
	f.ready.Store(true)

	if err := f.server.Serve(f.listener); !errors.Is(err, http.ErrServerClosed) {
		f.logger.Error(err.Error())
		os.Exit(1)
	}

	close(f.done)
}

func (f *Frontend) Join() {
	<-f.done
}

func (f *Frontend) CheckReady(ctx context.Context) bool {
	// Verify the DB is available and accessible
	if err := f.dbClient.DBConnectionTest(ctx); err != nil {
		f.logger.Error(fmt.Sprintf("Database test failed: %v", err))
		return false
	}
	f.logger.Debug("Database check completed")

	return f.ready.Load().(bool)
}

func (f *Frontend) NotFound(writer http.ResponseWriter, request *http.Request) {
	arm.WriteError(
		writer, http.StatusNotFound,
		arm.CloudErrorCodeNotFound, "",
		"The requested path could not be found.")
}

func (f *Frontend) Healthz(writer http.ResponseWriter, request *http.Request) {
	var healthStatus float64

	if f.CheckReady(request.Context()) {
		writer.WriteHeader(http.StatusOK)
		healthStatus = 1.0
	} else {
		arm.WriteInternalServerError(writer)
		healthStatus = 0.0
	}

	f.metrics.EmitGauge("frontend_health", healthStatus, map[string]string{
		"endpoint": "/healthz",
	})
}

func (f *Frontend) ArmResourceList(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var query string
	subscriptionId := request.PathValue(PathSegmentSubscriptionID)
	resourceGroupName := request.PathValue(PathSegmentResourceGroupName)
	location := request.PathValue(PageSegmentLocation)

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
	clustersRequest := f.clusterServiceConfig.Conn.ClustersMgmt().V2alpha1().Clusters().List().Search(query)
	clustersRequest.Size(pageSize)
	clustersRequest.Page(pageNumber)

	// Send the initial request:
	clustersListResponse, err := clustersRequest.Send()
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	systemData := &arm.SystemData{}
	var hcpCluster *api.HCPOpenShiftCluster
	var versionedHcpClusters []*api.VersionedHCPOpenShiftCluster
	clusters := clustersListResponse.Items().Slice()
	for _, cluster := range clusters {
		hcpCluster, err = f.ConvertCStoHCPOpenShiftCluster(systemData, cluster)
		if err != nil {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

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
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}
}

// ArmResourceRead implements the GET single resource API contract for ARM
// * 200 If the resource exists
// * 404 If the resource does not exist
func (f *Frontend) ArmResourceRead(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceRead", versionedInterface))

	doc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("existing document not found for cluster: %s", resourceID))
			arm.WriteResourceNotFoundError(writer, resourceID)
			return
		} else {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	}

	csCluster, err := f.clusterServiceConfig.GetCSCluster(doc.InternalID)
	if err != nil {
		f.logger.Error(fmt.Sprintf("cluster not found in clusters-service: %v", err))
		arm.WriteResourceNotFoundError(writer, resourceID)
		return
	}

	hcpCluster, err := f.ConvertCStoHCPOpenShiftCluster(doc.SystemData, csCluster)
	if err != nil {
		// Should never happen currently
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	versionedResource := versionedInterface.NewHCPOpenShiftCluster(hcpCluster)
	resp, err := json.Marshal(versionedResource)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}
}

func (f *Frontend) ArmResourceCreateOrUpdate(writer http.ResponseWriter, request *http.Request) {
	var err error

	// This handles both PUT and PATCH requests. PATCH requests will
	// never create a new resource. The only other notable difference
	// is the target struct that request bodies are overlayed onto:
	//
	// PUT requests overlay the request body onto a default resource
	// struct, which only has API-specified non-zero default values.
	// This means all required properties must be specified in the
	// request body, whether creating or updating a resource.
	//
	// PATCH requests overlay the request body onto a resource struct
	// that represents an existing resource to be updated.

	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceCreateOrUpdate", versionedInterface))

	doc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		f.logger.Error(fmt.Sprintf("failed to fetch document for %s: %v", resourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	var updating = (doc != nil)
	var docUpdated bool // whether to upsert the doc

	var versionedCurrentCluster api.VersionedHCPOpenShiftCluster
	var versionedRequestCluster api.VersionedHCPOpenShiftCluster
	var successStatusCode int

	if updating {
		// Note that because we found a database document for the cluster,
		// we expect Cluster Service to return us a cluster object.
		//
		// No special treatment here for "not found" errors. A "not found"
		// error indicates the database has gotten out of sync and so it's
		// appropriate to fail.
		csCluster, err := f.clusterServiceConfig.GetCSCluster(doc.InternalID)
		if err != nil {
			f.logger.Error(fmt.Sprintf("failed to fetch CS cluster for %s: %v", resourceID, err))
			arm.WriteInternalServerError(writer)
			return
		}

		hcpCluster, err := f.ConvertCStoHCPOpenShiftCluster(doc.SystemData, csCluster)
		if err != nil {
			// Should never happen currently
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		// This is slightly repetitive for the sake of clarity on PUT vs PATCH.
		switch request.Method {
		case http.MethodPut:
			versionedCurrentCluster = versionedInterface.NewHCPOpenShiftCluster(hcpCluster)
			versionedRequestCluster = versionedInterface.NewHCPOpenShiftCluster(nil)
			successStatusCode = http.StatusOK
		case http.MethodPatch:
			versionedCurrentCluster = versionedInterface.NewHCPOpenShiftCluster(hcpCluster)
			versionedRequestCluster = versionedInterface.NewHCPOpenShiftCluster(hcpCluster)
			successStatusCode = http.StatusAccepted
		}
	} else {
		switch request.Method {
		case http.MethodPut:
			versionedCurrentCluster = versionedInterface.NewHCPOpenShiftCluster(nil)
			versionedRequestCluster = versionedInterface.NewHCPOpenShiftCluster(nil)
			successStatusCode = http.StatusCreated
		case http.MethodPatch:
			// PATCH requests never create a new resource.
			f.logger.Error("Resource not found")
			arm.WriteResourceNotFoundError(writer, resourceID)
			return
		}

		doc = &database.ResourceDocument{
			ID:           uuid.New().String(),
			Key:          resourceID.String(),
			PartitionKey: resourceID.SubscriptionID,
		}
		docUpdated = true
	}

	body, err := BodyFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	if err = json.Unmarshal(body, versionedRequestCluster); err != nil {
		f.logger.Error(err.Error())
		arm.WriteCloudError(writer, arm.NewUnmarshalCloudError(err))
		return
	}

	cloudError := versionedRequestCluster.ValidateStatic(versionedCurrentCluster, updating, request.Method)
	if cloudError != nil {
		f.logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	hcpCluster := api.NewDefaultHCPOpenShiftCluster()
	versionedRequestCluster.Normalize(hcpCluster)

	hcpCluster.Name = request.PathValue(PathSegmentResourceName)
	csCluster, err := f.BuildCSCluster(ctx, hcpCluster, updating)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	if updating {
		f.logger.Info(fmt.Sprintf("updating resource %s", resourceID))
		_, err = f.clusterServiceConfig.UpdateCSCluster(doc.InternalID, csCluster)
		if err != nil {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	} else {
		f.logger.Info(fmt.Sprintf("creating resource %s", resourceID))
		csCluster, err = f.clusterServiceConfig.PostCSCluster(csCluster)
		if err != nil {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		doc.InternalID, err = ocm.NewInternalID(csCluster.HREF())
		if err != nil {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
		docUpdated = true
	}

	// Record the latest system data values from ARM, if present.
	if systemData != nil {
		doc.SystemData = systemData
		docUpdated = true
	}

	if docUpdated {
		err = f.dbClient.SetResourceDoc(ctx, doc)
		if err != nil {
			f.logger.Error(fmt.Sprintf("failed to upsert document for %s: %v", resourceID, err))
			arm.WriteInternalServerError(writer)
			return
		}
		f.logger.Info(fmt.Sprintf("document upserted for %s", resourceID))
	}

	resp, err := json.Marshal(versionedRequestCluster)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	writer.WriteHeader(successStatusCode)

	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}
}

// ArmResourceDelete implements the deletion API contract for ARM
// * 200 if a deletion is successful
// * 202 if an asynchronous delete is initiated
// * 204 if a well-formed request attempts to delete a nonexistent resource
func (f *Frontend) ArmResourceDelete(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceDelete", versionedInterface))

	var doc *database.ResourceDocument
	doc, err = f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Info(fmt.Sprintf("cluster document cannot be deleted -- document not found for %s", resourceID))
			writer.WriteHeader(http.StatusNoContent)
			return
		} else {
			f.logger.Error(fmt.Sprintf("failed to fetch document for %s: %v", resourceID, err))
			arm.WriteInternalServerError(writer)
			return
		}
	}

	err = f.clusterServiceConfig.DeleteCSCluster(doc.InternalID)
	if err != nil {
		f.logger.Error(fmt.Sprintf("failed to delete cluster %s: %v", resourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	err = f.dbClient.DeleteResourceDoc(ctx, resourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Info(fmt.Sprintf("cluster document cannot be deleted -- document not found for %s", resourceID))
			writer.WriteHeader(http.StatusNoContent)
			return
		} else {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	}
	f.logger.Info(fmt.Sprintf("document deleted for resource %s", resourceID))

	// TODO: Eventually this will be an asynchronous delete and need to return a 202
	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmResourceAction(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceAction", versionedInterface))

	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmSubscriptionGet(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	doc, err := f.dbClient.GetSubscriptionDoc(ctx, subscriptionID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("document not found for subscription %s", subscriptionID))
			arm.WriteResourceNotFoundError(writer, resourceID)
			return
		} else {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	}

	resp, err := json.Marshal(&doc.Subscription)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}
}

func (f *Frontend) ArmSubscriptionPut(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	body, err := BodyFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var subscription arm.Subscription
	err = json.Unmarshal(body, &subscription)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteCloudError(writer, arm.NewUnmarshalCloudError(err))
		return
	}

	cloudError := api.ValidateSubscription(&subscription)
	if cloudError != nil {
		f.logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	var doc *database.SubscriptionDocument
	doc, err = f.dbClient.GetSubscriptionDoc(ctx, subscriptionID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Info(fmt.Sprintf("existing document not found for subscription - creating one for %s", subscriptionID))
			doc = &database.SubscriptionDocument{
				ID:           uuid.New().String(),
				PartitionKey: subscriptionID,
				Subscription: &subscription,
			}
		} else {
			f.logger.Error("failed to fetch document for %s: %v", subscriptionID, err)
			arm.WriteInternalServerError(writer)
			return
		}
	} else {
		f.logger.Info(fmt.Sprintf("existing document found for subscription - will update document for subscription %s", subscriptionID))
		doc.Subscription = &subscription

		messages := getSubscriptionDifferences(doc.Subscription, &subscription)
		for _, message := range messages {
			f.logger.Info(message)
		}
	}

	err = f.dbClient.SetSubscriptionDoc(ctx, doc)
	if err != nil {
		f.logger.Error("failed to create document for subscription %s: %v", subscriptionID, err)
	}

	f.metrics.EmitGauge("subscription_lifecycle", 1, map[string]string{
		"location":       f.location,
		"subscriptionid": subscriptionID,
		"state":          string(subscription.State),
	})

	resp, err := json.Marshal(subscription)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	writer.WriteHeader(http.StatusCreated)
	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}
}

func (f *Frontend) ArmDeploymentPreflight(writer http.ResponseWriter, request *http.Request) {
	var subscriptionID string = request.PathValue(PathSegmentSubscriptionID)
	var resourceGroup string = request.PathValue(PathSegmentResourceGroupName)
	var apiVersion string = request.URL.Query().Get("api-version")

	ctx := request.Context()

	f.logger.Info(fmt.Sprintf("%s: ArmDeploymentPreflight", apiVersion))

	body, err := BodyFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	deploymentPreflight, cloudError := arm.UnmarshalDeploymentPreflight(body)
	if cloudError != nil {
		f.logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	validate := api.NewValidator()
	preflightErrors := []arm.CloudErrorBody{}

	for index, raw := range deploymentPreflight.Resources {
		resource := &arm.DeploymentPreflightResource{}
		err = json.Unmarshal(raw, resource)
		if err != nil {
			cloudError = arm.NewUnmarshalCloudError(err)
			// Preflight is best-effort: a malformed resource is not a validation failure.
			f.logger.Warn(cloudError.Message)
		}

		// This is just "preliminary" validation to ensure all the base resource
		// fields are present and the API version is valid.
		resourceErrors := api.ValidateRequest(validate, request.Method, resource)
		if len(resourceErrors) > 0 {
			// Preflight is best-effort: a malformed resource is not a validation failure.
			f.logger.Warn(
				fmt.Sprintf("Resource #%d failed preliminary validation (see details)", index+1),
				"details", resourceErrors)
			continue
		}

		// API version is already validated by this point.
		versionedInterface, _ := api.Lookup(resource.APIVersion)
		versionedCluster := versionedInterface.NewHCPOpenShiftCluster(nil)

		err = json.Unmarshal(raw, versionedCluster)
		if err != nil {
			// Preflight is best effort: failure to parse a resource is not a validation failure.
			f.logger.Warn(fmt.Sprintf("Failed to unmarshal %s resource named '%s': %s", resource.Type, resource.Name, err))
			continue
		}

		// Perform static validation as if for a cluster creation request.
		cloudError := versionedCluster.ValidateStatic(versionedCluster, false, http.MethodPut)
		if cloudError != nil {
			var details []arm.CloudErrorBody

			// This avoids double-nesting details when there's multiple errors.
			//
			// To illustrate, instead of:
			//
			// {
			//   "code": "MultipleErrorsOccurred"
			//   "message": "Content validation failed for {{RESOURCE_NAME}}"
			//   "target": "{{RESOURCE_ID}}"
			//   "details": [
			//     {
			//       "code": "MultipleErrorsOccurred"
			//       "message": "Content validation failed on multiple fields"
			//       "details": [
			//         ...field-specific validation errors...
			//       ]
			//     }
			//   ]
			// }
			//
			// we want:
			//
			// {
			//   "code": "MultipleErrorsOccurred"
			//   "message": "Content validation failed for {{RESOURCE_NAME}}"
			//   "target": "{{RESOURCE_ID}}"
			//   "details": [
			//     ...field-specific validation errors...
			//   ]
			// }
			//
			if len(cloudError.CloudErrorBody.Details) > 0 {
				details = cloudError.CloudErrorBody.Details
			} else {
				details = []arm.CloudErrorBody{*cloudError.CloudErrorBody}
			}
			preflightErrors = append(preflightErrors, arm.CloudErrorBody{
				Code:    cloudError.Code,
				Message: fmt.Sprintf("Content validation failed for '%s'", resource.Name),
				Target:  resource.ResourceID(subscriptionID, resourceGroup),
				Details: details,
			})
			continue
		}

		// FIXME Further preflight steps go here.
	}

	arm.WriteDeploymentPreflightResponse(writer, preflightErrors)
}

func (f *Frontend) CreateOrUpdateNodePool(writer http.ResponseWriter, request *http.Request) {
	var err error

	// This handles both PUT and PATCH requests. PATCH requests will
	// never create a new resource. The only other notable difference
	// is the target struct that request bodies are overlayed onto:
	//
	// PUT requests overlay the request body onto a default resource
	// struct, which only has API-specified non-zero default values.
	// This means all required properties must be specified in the
	// request body, whether creating or updating a resource.
	//
	// PATCH requests overlay the request body onto a resource struct
	// that represents an existing resource to be updated.

	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	nodePoolResourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: CreateNodePool", versionedInterface))

	clusterResourceID := nodePoolResourceID.Parent
	if clusterResourceID == nil {
		f.logger.Error(fmt.Sprintf("failed to obtain Azure parent resourceID for node pool %s", nodePoolResourceID))
		arm.WriteInternalServerError(writer)
		return
	}

	clusterDoc, err := f.dbClient.GetResourceDoc(ctx, clusterResourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("existing document not found for cluster %s when creating node pool", clusterResourceID))
			arm.WriteInternalServerError(writer)
			return
		}
		f.logger.Error(fmt.Sprintf("failed to fetch cluster document for %s when creating node pool: %v", clusterResourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	csCluster, err := f.clusterServiceConfig.GetCSCluster(clusterDoc.InternalID)
	if err != nil {
		f.logger.Error(fmt.Sprintf("failed to fetch CS cluster for %s: %v", clusterResourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	if csCluster.State() == cmv2alpha1.ClusterStateUninstalling {
		f.logger.Error(fmt.Sprintf("failed to create node pool for cluster %s as it is in %v state", clusterResourceID, cmv2alpha1.ClusterStateUninstalling))
		arm.WriteInternalServerError(writer)
		return
	}

	nodePoolDoc, err := f.dbClient.GetResourceDoc(ctx, nodePoolResourceID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		f.logger.Error(fmt.Sprintf("failed to fetch document for %s: %v", nodePoolResourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	var updating = (nodePoolDoc != nil)
	var docUpdated bool // whether to upsert the doc

	var versionedCurrentNodePool api.VersionedHCPOpenShiftClusterNodePool
	var versionedRequestNodePool api.VersionedHCPOpenShiftClusterNodePool
	var successStatusCode int

	if updating {
		// Note that because we found a database document for the cluster,
		// we expect Cluster Service to return us a node pool object.
		//
		// No special treatment here for "not found" errors. A "not found"
		// error indicates the database has gotten out of sync and so it's
		// appropriate to fail.
		csNodePool, err := f.clusterServiceConfig.GetCSNodePool(nodePoolDoc.InternalID)
		if err != nil {
			f.logger.Error(fmt.Sprintf("failed to fetch CS node pool for %s: %v", nodePoolResourceID, err))
			arm.WriteInternalServerError(writer)
			return
		}

		hcpNodePool, err := f.ConvertCStoNodePool(ctx, nodePoolDoc.SystemData, csNodePool)
		if err != nil {
			// Should never happen currently
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		// This is slightly repetitive for the sake of clarify on PUT vs PATCH.
		switch request.Method {
		case http.MethodPut:
			versionedCurrentNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
			versionedRequestNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(nil)
			successStatusCode = http.StatusOK
		case http.MethodPatch:
			versionedCurrentNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
			versionedRequestNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
			successStatusCode = http.StatusAccepted
		}
	} else {
		switch request.Method {
		case http.MethodPut:
			versionedCurrentNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(nil)
			versionedRequestNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(nil)
			successStatusCode = http.StatusCreated
		case http.MethodPatch:
			// PATCH requests never create a new resource.
			f.logger.Error("Resource not found")
			arm.WriteResourceNotFoundError(writer, nodePoolResourceID)
			return
		}

		nodePoolDoc = &database.ResourceDocument{
			ID:           uuid.New().String(),
			Key:          nodePoolResourceID.String(),
			PartitionKey: nodePoolResourceID.SubscriptionID,
		}
		docUpdated = true
	}

	body, err := BodyFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	if err = json.Unmarshal(body, versionedRequestNodePool); err != nil {
		f.logger.Error(err.Error())
		arm.WriteCloudError(writer, arm.NewUnmarshalCloudError(err))
		return
	}

	cloudError := versionedRequestNodePool.ValidateStatic(versionedCurrentNodePool, updating, request.Method)
	if cloudError != nil {
		f.logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	hcpNodePool := api.NewDefaultHCPOpenShiftClusterNodePool()
	versionedRequestNodePool.Normalize(hcpNodePool)

	hcpNodePool.Name = request.PathValue(PathSegmentNodePoolName)
	csNodePool, err := f.BuildCSNodePool(ctx, hcpNodePool, updating)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	if updating {
		f.logger.Info(fmt.Sprintf("updating resource %s", nodePoolResourceID))
		_, err = f.clusterServiceConfig.UpdateCSNodePool(nodePoolDoc.InternalID, csNodePool)
		if err != nil {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	} else {
		f.logger.Info(fmt.Sprintf("creating resource %s", nodePoolResourceID))
		csNodePool, err = f.clusterServiceConfig.PostCSNodePool(clusterDoc.InternalID, csNodePool)
		if err != nil {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		nodePoolDoc.InternalID, err = ocm.NewInternalID(csNodePool.HREF())
		if err != nil {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
		docUpdated = true
	}

	// Record the latest system data values from ARM, if present.
	if systemData != nil {
		nodePoolDoc.SystemData = systemData
		docUpdated = true
	}

	if docUpdated {
		err = f.dbClient.SetResourceDoc(ctx, nodePoolDoc)
		if err != nil {
			f.logger.Error(fmt.Sprintf("failed to upsert document for %s: %v", nodePoolResourceID, err))
			arm.WriteInternalServerError(writer)
			return
		}
		f.logger.Info(fmt.Sprintf("document upserted for %s", nodePoolResourceID))
	}

	resp, err := json.Marshal(versionedRequestNodePool)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	writer.WriteHeader(successStatusCode)

	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}
}

func (f *Frontend) GetNodePool(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	nodePoolResourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: GetNodePools", versionedInterface))

	clusterResourceID := nodePoolResourceID.Parent
	if clusterResourceID == nil {
		f.logger.Error(fmt.Sprintf("failed to obtain Azure parent resourceID for node pool %s", nodePoolResourceID))
		arm.WriteInternalServerError(writer)
		return
	}

	nodePoolDoc, err := f.dbClient.GetResourceDoc(ctx, nodePoolResourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("existing node pool document not found for node pool: %s on GET node pool by name", nodePoolResourceID))
			writer.WriteHeader(http.StatusNoContent)
			return
		}

		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	nodePool, err := f.clusterServiceConfig.GetCSNodePool(nodePoolDoc.InternalID)
	if err != nil {
		f.logger.Error(fmt.Sprintf("node pool not found in clusters-service: %v", err))
		writer.WriteHeader(http.StatusNoContent)
		return
	}

	aroNodePool, err := f.ConvertCStoNodePool(ctx, nodePoolDoc.SystemData, nodePool)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	versionedNodePool := versionedInterface.NewHCPOpenShiftClusterNodePool(aroNodePool)
	resp, err := json.Marshal(versionedNodePool)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}
}

func (f *Frontend) DeleteNodePool(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	nodePoolResourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: DeleteNodePool", versionedInterface))

	clusterResourceID := nodePoolResourceID.Parent
	if clusterResourceID == nil {
		f.logger.Error(fmt.Sprintf("failed to obtain Azure parent resourceID for node pool %s", nodePoolResourceID))
		arm.WriteInternalServerError(writer)
		return
	}

	doc, err := f.dbClient.GetResourceDoc(ctx, nodePoolResourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("node pool document cannot be deleted -- node pool document not found for %s", nodePoolResourceID))
			writer.WriteHeader(http.StatusNoContent)
			return
		}

		f.logger.Error(fmt.Sprintf("failed to fetch node pool document for %s: %v", nodePoolResourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	err = f.clusterServiceConfig.DeleteCSNodePool(doc.InternalID)
	if err != nil {
		f.logger.Error(fmt.Sprintf("failed to delete node pool %s: %v", nodePoolResourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	err = f.dbClient.DeleteResourceDoc(ctx, nodePoolResourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("node pool document cannot be deleted -- node pool document not found for %s", nodePoolResourceID))
			writer.WriteHeader(http.StatusNoContent)
			return
		}

		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("node pool document deleted for resource %s", nodePoolResourceID))

	writer.WriteHeader(http.StatusAccepted)
}

func getSubscriptionDifferences(oldSub, newSub *arm.Subscription) []string {
	var messages []string

	if oldSub.State != newSub.State {
		messages = append(messages, fmt.Sprintf("Subscription state changed from %s to %s", oldSub.State, newSub.State))
	}

	if oldSub.Properties != nil && newSub.Properties != nil {
		if oldSub.Properties.TenantId != nil && newSub.Properties.TenantId != nil &&
			*oldSub.Properties.TenantId != *newSub.Properties.TenantId {
			messages = append(messages, fmt.Sprintf("Subscription tenantId changed from %s to %s", *oldSub.Properties.TenantId, *newSub.Properties.TenantId))
		}

		if oldSub.Properties.RegisteredFeatures != nil && newSub.Properties.RegisteredFeatures != nil {
			oldFeatures := featuresMap(oldSub.Properties.RegisteredFeatures)
			newFeatures := featuresMap(newSub.Properties.RegisteredFeatures)

			for featureName, oldState := range oldFeatures {
				newState, exists := newFeatures[featureName]
				if !exists {
					messages = append(messages, fmt.Sprintf("Feature %s removed", featureName))
				} else if oldState != newState {
					messages = append(messages, fmt.Sprintf("Feature %s state changed from %s to %s", featureName, oldState, newState))
				}
			}
			for featureName, newState := range newFeatures {
				if _, exists := oldFeatures[featureName]; !exists {
					messages = append(messages, fmt.Sprintf("Feature %s added with state %s", featureName, newState))
				}
			}
		}
	}

	return messages
}

func featuresMap(features *[]arm.Feature) map[string]string {
	if features == nil {
		return nil
	}
	featureMap := make(map[string]string, len(*features))
	for _, feature := range *features {
		if feature.Name != nil && feature.State != nil {
			featureMap[*feature.Name] = *feature.State
		}
	}
	return featureMap
}

// Function to build the NextLink URL with pagination parameters
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
