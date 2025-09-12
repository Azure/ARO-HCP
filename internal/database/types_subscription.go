package database

import (
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type SubscriptionWrapper struct {
	TypedDocument `json:",inline"`
	Properties    arm.Subscription `json:"properties"`
}

var _ TypedResource = &SubscriptionWrapper{}

func (doc *SubscriptionWrapper) GetTypedDocument() *TypedDocument {
	return &doc.TypedDocument
}

func (doc *SubscriptionWrapper) GetSubscriptionID() string {
	return doc.TypedDocument.PartitionKey
}

func (doc *SubscriptionWrapper) GetReportingID() string {
	return doc.GetTypedDocument().ID
}

func (doc *SubscriptionWrapper) SetTypedDocument(in TypedDocument) {
	doc.TypedDocument = in
}
