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

package database

import (
	"context"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/tracing"
)

// dbClientWithInstrumentation instruments a DBClient instance with traces.
type dbClientWithInstrumentation struct {
	dbClient   DBClient
	tracerName string
}

// NewDBClientWithInstrumentation returns a DBClient instrumented for tracing.
func NewDBClientWithInstrumentation(dbClient DBClient, tracerName string) (DBClient, error) {
	if tracerName == "" {
		return nil, fmt.Errorf("tracer name cannot be empty")
	}

	return &dbClientWithInstrumentation{
		dbClient:   dbClient,
		tracerName: tracerName,
	}, nil
}

func (d *dbClientWithInstrumentation) newSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return trace.SpanFromContext(ctx).
		TracerProvider().
		Tracer(d.tracerName).
		Start(ctx, fmt.Sprintf("DBClient.%s", name))
}

func setResourceDocAttributes(span trace.Span, doc *ResourceDocument) {
	if doc == nil {
		return
	}

	span.SetAttributes(
		tracing.ResourceIDKey.String(doc.ResourceID.String()),
		tracing.ClustersServiceResourceIDKey.String(doc.InternalID.ID()),
		tracing.ClustersServiceResourceKindKey.String(doc.InternalID.Kind()),
		tracing.OperationStatusKey.String(string(doc.ProvisioningState)),
	)

	if doc.ActiveOperationID != "" {
		span.SetAttributes(
			tracing.OperationIDKey.String(doc.ActiveOperationID),
		)
	}
}

func setOperationDocAttributes(span trace.Span, doc *OperationDocument) {
	if doc == nil {
		return
	}

	span.SetAttributes(
		tracing.ResourceIDKey.String(doc.ExternalID.String()),
		tracing.ClustersServiceResourceIDKey.String(doc.InternalID.ID()),
		tracing.ClustersServiceResourceKindKey.String(doc.InternalID.Kind()),
		tracing.OperationIDKey.String(doc.OperationID.String()),
		tracing.OperationTypeKey.String(string(doc.Request)),
		tracing.OperationStatusKey.String(string(doc.Status)),
	)
}

func (d *dbClientWithInstrumentation) DBConnectionTest(ctx context.Context) error {
	return d.dbClient.DBConnectionTest(ctx)
}

func (d *dbClientWithInstrumentation) GetLockClient() *LockClient {
	return d.dbClient.GetLockClient()
}

func (d *dbClientWithInstrumentation) NewTransaction(pk azcosmos.PartitionKey) DBTransaction {
	return d.dbClient.NewTransaction(pk)
}

func (d *dbClientWithInstrumentation) GetResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) (string, *ResourceDocument, error) {
	ctx, span := d.newSpan(ctx, "GetResourceDoc")
	defer span.End()
	span.SetAttributes(tracing.ResourceIDKey.String(resourceID.String()))

	id, doc, err := d.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		span.RecordError(err)
	} else {
		setResourceDocAttributes(span, doc)
	}

	return id, doc, err
}

func (d *dbClientWithInstrumentation) CreateResourceDoc(ctx context.Context, doc *ResourceDocument) error {
	ctx, span := d.newSpan(ctx, "CreateResourceDoc")
	defer span.End()
	setResourceDocAttributes(span, doc)

	err := d.dbClient.CreateResourceDoc(ctx, doc)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (d *dbClientWithInstrumentation) PatchResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID, ops ResourceDocumentPatchOperations) (*ResourceDocument, error) {
	ctx, span := d.newSpan(ctx, "PatchResourceDoc")
	defer span.End()
	span.SetAttributes(tracing.ResourceIDKey.String(resourceID.String()))

	doc, err := d.dbClient.PatchResourceDoc(ctx, resourceID, ops)
	if err != nil {
		span.RecordError(err)
	}
	setResourceDocAttributes(span, doc)

	return doc, err
}

func (d *dbClientWithInstrumentation) DeleteResourceDoc(ctx context.Context, resourceID *azcorearm.ResourceID) error {
	ctx, span := d.newSpan(ctx, "DeleteResourceDoc")
	defer span.End()
	span.SetAttributes(tracing.ResourceIDKey.String(resourceID.String()))

	err := d.dbClient.DeleteResourceDoc(ctx, resourceID)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (d *dbClientWithInstrumentation) ListResourceDocs(prefix *azcorearm.ResourceID, maxItems int32, continuationToken *string) DBClientIterator[ResourceDocument] {
	// TODO: pass a context.Context argument to trace the function?
	return d.dbClient.ListResourceDocs(prefix, maxItems, continuationToken)
}

func (d *dbClientWithInstrumentation) GetOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string) (*OperationDocument, error) {
	ctx, span := d.newSpan(ctx, "GetOperationDoc")
	defer span.End()

	doc, err := d.dbClient.GetOperationDoc(ctx, pk, operationID)
	if err != nil {
		span.RecordError(err)
	}
	setOperationDocAttributes(span, doc)

	return doc, err

}

func (d *dbClientWithInstrumentation) CreateOperationDoc(ctx context.Context, doc *OperationDocument) (string, error) {
	ctx, span := d.newSpan(ctx, "CreateOperationDoc")
	defer span.End()
	setOperationDocAttributes(span, doc)

	id, err := d.dbClient.CreateOperationDoc(ctx, doc)
	if err != nil {
		span.RecordError(err)
	}

	return id, err
}

func (d *dbClientWithInstrumentation) PatchOperationDoc(ctx context.Context, pk azcosmos.PartitionKey, operationID string, ops OperationDocumentPatchOperations) (*OperationDocument, error) {
	ctx, span := d.newSpan(ctx, "PatchOperationDoc")
	defer span.End()

	doc, err := d.dbClient.PatchOperationDoc(ctx, pk, operationID, ops)
	if err != nil {
		span.RecordError(err)
	}

	return doc, err
}

func (d *dbClientWithInstrumentation) ListActiveOperationDocs(pk azcosmos.PartitionKey, options *DBClientListActiveOperationDocsOptions) DBClientIterator[OperationDocument] {
	// TODO: pass a context.Context argument to trace the function?
	return d.dbClient.ListActiveOperationDocs(pk, options)
}

func (d *dbClientWithInstrumentation) GetSubscriptionDoc(ctx context.Context, subscriptionID string) (*arm.Subscription, error) {
	return d.dbClient.GetSubscriptionDoc(ctx, subscriptionID)
}

func (d *dbClientWithInstrumentation) CreateSubscriptionDoc(ctx context.Context, subscriptionID string, subscription *arm.Subscription) error {
	return d.dbClient.CreateSubscriptionDoc(ctx, subscriptionID, subscription)
}

func (d *dbClientWithInstrumentation) UpdateSubscriptionDoc(ctx context.Context, subscriptionID string, callback func(*arm.Subscription) bool) (bool, error) {
	return d.dbClient.UpdateSubscriptionDoc(ctx, subscriptionID, callback)
}

func (d *dbClientWithInstrumentation) ListAllSubscriptionDocs() DBClientIterator[arm.Subscription] {
	return d.dbClient.ListAllSubscriptionDocs()
}
