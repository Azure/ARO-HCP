// Copyright 2026 Microsoft Corporation
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

package cosmosstorageutils

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// NewLayeredTransaction applies layer to Execute. Step assembly, partition-key
// access, and success callback registration are forwarded unchanged. The layer
// uses Execute's context; it does not inherit layers from the CRUDs adding steps.
//
// Use the same RU bucket or controller budget layer as related CRUDs to share
// their budget. The Cosmos HTTP policy charges the batch's response once per
// attempt, rather than charging each step separately.
func NewLayeredTransaction(inner DBTransaction, layer ResourceCRUDLayer) DBTransaction {
	return &layeredTransaction{DBTransaction: inner, layer: layer}
}

type layeredTransaction struct {
	DBTransaction
	layer ResourceCRUDLayer
}

func (t *layeredTransaction) Execute(ctx context.Context, options *azcosmos.TransactionalBatchOptions) (DBTransactionResult, error) {
	return layeredResult(ctx, t.layer, func(ctx context.Context) (DBTransactionResult, error) {
		return t.DBTransaction.Execute(ctx, options)
	})
}
