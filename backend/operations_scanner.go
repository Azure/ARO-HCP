package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	ocmsdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	defaultCosmosOperationsPollInterval = 30 * time.Second
	defaultClusterServicePollInterval   = 10 * time.Second
)

type OperationsScanner struct {
	dbClient           database.DBClient
	lockClient         *database.LockClient
	clusterService     ocm.ClusterServiceClient
	activeOperations   []*database.OperationDocument
	notificationClient *http.Client
	done               chan struct{}
}

func NewOperationsScanner(dbClient database.DBClient, ocmConnection *ocmsdk.Connection) *OperationsScanner {
	return &OperationsScanner{
		dbClient:           dbClient,
		lockClient:         dbClient.GetLockClient(),
		clusterService:     ocm.ClusterServiceClient{Conn: ocmConnection},
		activeOperations:   make([]*database.OperationDocument, 0),
		notificationClient: http.DefaultClient,
		done:               make(chan struct{}),
	}
}

func getInterval(envName string, defaultVal time.Duration, logger *slog.Logger) time.Duration {
	if intervalString, ok := os.LookupEnv(envName); ok {
		interval, err := time.ParseDuration(intervalString)
		if err == nil {
			return interval
		} else {
			logger.Warn(fmt.Sprintf("Cannot use %s: %s", envName, err.Error()))
		}
	}
	return defaultVal
}

func (s *OperationsScanner) Run(ctx context.Context, logger *slog.Logger) {
	defer close(s.done)

	var interval time.Duration

	interval = getInterval("COSMOS_OPERATIONS_POLL_INTERVAL", defaultCosmosOperationsPollInterval, logger)
	logger.Info("Polling Cosmos Operations items every " + interval.String())
	pollDBOperationsTicker := time.NewTicker(interval)

	interval = getInterval("CLUSTER_SERVICE_POLL_INTERVAL", defaultClusterServicePollInterval, logger)
	logger.Info("Polling Cluster Service every " + interval.String())
	pollCSOperationsTicker := time.NewTicker(interval)

	// Poll database immediately on startup.
	s.pollDBOperations(ctx, logger)

loop:
	for {
		select {
		case <-pollDBOperationsTicker.C:
			s.pollDBOperations(ctx, logger)
		case <-pollCSOperationsTicker.C:
			s.pollCSOperations(logger)
		case <-ctx.Done():
			// break alone just breaks out of select.
			// Use a label to break out of the loop.
			break loop
		}
	}
}

func (s *OperationsScanner) Join() {
	<-s.done
}

func (s *OperationsScanner) pollDBOperations(ctx context.Context, logger *slog.Logger) {
	var activeOperations []*database.OperationDocument

	iterator := s.dbClient.ListAllOperationDocs()

	for doc := range iterator.Items(ctx) {
		if !doc.Status.IsTerminal() {
			activeOperations = append(activeOperations, doc)
		}
	}

	err := iterator.GetError()
	if err == nil {
		s.activeOperations = activeOperations
		if len(s.activeOperations) > 0 {
			logger.Info(fmt.Sprintf("Tracking %d active operations", len(s.activeOperations)))
		}
	} else {
		logger.Error(fmt.Sprintf("Error while paging through Cosmos query results: %s", err.Error()))
	}
}

func (s *OperationsScanner) pollCSOperations(logger *slog.Logger) {
	var activeOperations []*database.OperationDocument

	// We use a separate context here so that if the process receives a
	// terminate signal, the backend can finish processing an operation
	// before terminating.
	//
	// This is necessary in the absence of database transactions, though
	// still not foolproof, to try to ensure consistency between resource
	// and operation documents in Cosmos DB.
	//
	// Database transactions would be the preferred solution and we're
	// working toward that.
	ctx := context.Background()

	for _, doc := range s.activeOperations {
		var requeue bool
		var err error

		opLogger := logger.With(
			"operation", doc.Request,
			"operation_id", doc.ID,
			"resource_id", doc.ExternalID.String(),
			"internal_id", doc.InternalID.String())

		switch doc.InternalID.Kind() {
		case cmv1.ClusterKind:
			requeue, err = s.pollClusterOperation(ctx, opLogger, doc)
		case cmv1.NodePoolKind:
			requeue, err = s.pollNodePoolOperation(ctx, opLogger, doc)
		}
		if requeue {
			activeOperations = append(activeOperations, doc)
		}
		if err != nil {
			opLogger.Error(fmt.Sprintf("Error while polling operation '%s': %s", doc.ID, err.Error()))
		}
	}

	s.activeOperations = activeOperations
}

func (s *OperationsScanner) pollClusterOperation(ctx context.Context, logger *slog.Logger, doc *database.OperationDocument) (bool, error) {
	var requeue bool = true

	clusterStatus, err := s.clusterService.GetCSClusterStatus(ctx, doc.InternalID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound && doc.Request == database.OperationRequestDelete {
			err = s.withSubscriptionLock(ctx, logger, doc.ExternalID.SubscriptionID, func(ctx context.Context) error {
				return s.deleteOperationCompleted(ctx, logger, doc)
			})
			if err == nil {
				requeue = false
			}
		}
		return requeue, err
	}

	opStatus, opError, err := convertClusterStatus(clusterStatus, doc.Status)
	if err != nil {
		logger.Warn(err.Error())
		err = nil
	} else {
		err = s.withSubscriptionLock(ctx, logger, doc.ExternalID.SubscriptionID, func(ctx context.Context) error {
			return s.updateOperationStatus(ctx, logger, doc, opStatus, opError)
		})
	}

	return requeue, err
}

func (s *OperationsScanner) pollNodePoolOperation(ctx context.Context, logger *slog.Logger, doc *database.OperationDocument) (bool, error) {
	var requeue bool = true
	// FIXME Implement when new OCM API is available.
	return requeue, nil
}

func (s *OperationsScanner) withSubscriptionLock(ctx context.Context, logger *slog.Logger, subscriptionID string, fn func(ctx context.Context) error) error {
	timeout := s.lockClient.GetDefaultTimeToLive()
	lock, err := s.lockClient.AcquireLock(ctx, subscriptionID, &timeout)
	if err != nil {
		return fmt.Errorf("Failed to acquire lock for subscription '%s': %w", subscriptionID, err)
	}

	lockedCtx, stop := s.lockClient.HoldLock(ctx, lock)
	err = fn(lockedCtx)
	lock = stop()

	if lock != nil {
		nonFatalErr := s.lockClient.ReleaseLock(ctx, lock)
		if nonFatalErr != nil {
			// Failure here is non-fatal but still log the error.
			// The lock's TTL ensures it will be released eventually.
			logger.Error(fmt.Sprintf("Failed to release lock for subscription '%s': %v", subscriptionID, nonFatalErr))
		}
	}

	return err
}

func (s *OperationsScanner) deleteOperationCompleted(ctx context.Context, logger *slog.Logger, doc *database.OperationDocument) error {
	err := s.dbClient.DeleteResourceDoc(ctx, doc.ExternalID)
	if err != nil {
		return err
	}

	// Save a final "succeeded" operation status until TTL expires.
	const opStatus arm.ProvisioningState = arm.ProvisioningStateSucceeded
	updated, err := s.dbClient.UpdateOperationDoc(ctx, doc.ID, func(updateDoc *database.OperationDocument) bool {
		return updateDoc.UpdateStatus(opStatus, nil)
	})
	if err != nil {
		return err
	}
	if updated {
		logger.Info(fmt.Sprintf("Updated Operations container item for '%s' with status '%s'", doc.ID, opStatus))
		s.maybePostAsyncNotification(ctx, logger, doc)
	}

	return nil
}

func (s *OperationsScanner) updateOperationStatus(ctx context.Context, logger *slog.Logger, doc *database.OperationDocument, opStatus arm.ProvisioningState, opError *arm.CloudErrorBody) error {
	updated, err := s.dbClient.UpdateOperationDoc(ctx, doc.ID, func(updateDoc *database.OperationDocument) bool {
		return updateDoc.UpdateStatus(opStatus, opError)
	})
	if err != nil {
		return err
	}
	if updated {
		logger.Info(fmt.Sprintf("Updated Operations container item for '%s' with status '%s'", doc.ID, opStatus))
		s.maybePostAsyncNotification(ctx, logger, doc)
	}

	updated, err = s.dbClient.UpdateResourceDoc(ctx, doc.ExternalID, func(updateDoc *database.ResourceDocument) bool {
		var updated bool

		if doc.ID == updateDoc.ActiveOperationID {
			if opStatus != updateDoc.ProvisioningState {
				updateDoc.ProvisioningState = opStatus
				updated = true
			}
			if opStatus.IsTerminal() {
				updateDoc.ActiveOperationID = ""
				updated = true
			}
		}

		return updated
	})
	if err != nil {
		return err
	}
	if updated {
		logger.Info(fmt.Sprintf("Updated Resources container item for '%s' with provisioning state '%s'", doc.ExternalID, opStatus))
	}

	return nil
}

func (s *OperationsScanner) maybePostAsyncNotification(ctx context.Context, logger *slog.Logger, doc *database.OperationDocument) {
	if len(doc.NotificationURI) > 0 {
		err := s.postAsyncNotification(ctx, doc.NotificationURI)
		if err == nil {
			logger.Info(fmt.Sprintf("Posted async notification for operation '%s'", doc.ID))
		} else {
			logger.Error(fmt.Sprintf("Failed to post async notification for operation '%s': %s", doc.ID, err.Error()))
		}
	}
}

func (s *OperationsScanner) postAsyncNotification(ctx context.Context, url string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}

	response, err := s.notificationClient.Do(request)
	if err != nil {
		return err
	}

	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return errors.New(response.Status)
	}

	return nil
}

func convertClusterStatus(clusterStatus *arohcpv1alpha1.ClusterStatus, current arm.ProvisioningState) (arm.ProvisioningState, *arm.CloudErrorBody, error) {
	var opStatus arm.ProvisioningState = current
	var opError *arm.CloudErrorBody
	var err error

	// FIXME This logic is all tenative until the new "/api/aro_hcp/v1" OCM
	//       API is available. What's here now is a best guess at converting
	//       ClusterStatus from the "/api/clusters_mgmt/v1" API.

	switch state := clusterStatus.State(); state {
	case arohcpv1alpha1.ClusterStateError:
		opStatus = arm.ProvisioningStateFailed
		// FIXME This is guesswork. Need clarity from Cluster Service
		//       on what provision error codes are possible so we can
		//       translate to an appropriate cloud error code.
		code := clusterStatus.ProvisionErrorCode()
		if code == "" {
			code = arm.CloudErrorCodeInternalServerError
		}
		message := clusterStatus.ProvisionErrorMessage()
		if message == "" {
			message = clusterStatus.Description()
		}
		opError = &arm.CloudErrorBody{Code: code, Message: message}
	case arohcpv1alpha1.ClusterStateInstalling:
		opStatus = arm.ProvisioningStateProvisioning
	case arohcpv1alpha1.ClusterStateReady:
		opStatus = arm.ProvisioningStateSucceeded
	case arohcpv1alpha1.ClusterStateUninstalling:
		opStatus = arm.ProvisioningStateDeleting
	case arohcpv1alpha1.ClusterStatePending, arohcpv1alpha1.ClusterStateValidating:
		// These are valid cluster states for ARO-HCP but there are
		// no unique ProvisioningState values for them. They should
		// only occur when ProvisioningState is Accepted.
		if current != arm.ProvisioningStateAccepted {
			err = fmt.Errorf("Got ClusterState '%s' while ProvisioningState was '%s' instead of '%s'", state, current, arm.ProvisioningStateAccepted)
		}
	default:
		err = fmt.Errorf("Unhandled ClusterState '%s'", state)
	}

	return opStatus, opError, err
}
