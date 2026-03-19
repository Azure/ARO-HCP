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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	operationLabelCreate  = "create"
	operationLabelReplace = "replace"
	operationLabelDelete  = "delete"
	operationLabelGet     = "get"
	operationLabelList    = "list"
)

var (
	// DatabaseOperationTotal counts the total number of database operations attempted.
	DatabaseOperationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "database_operation_total",
			Help: "Total number of database operations attempted by operation type and resource type.",
		},
		[]string{"operation", "resource_type"},
	)

	// DatabaseTransactionOperationTotal counts the total number of database transaction operations attempted.
	DatabaseTransactionOperationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "database_transaction_operation_total",
			Help: "Total number of database transaction operations attempted by operation type and resource type.",
		},
		[]string{"operation", "resource_type"},
	)

	// DatabaseErrorTotal counts errors from database operations.
	DatabaseErrorTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "database_error_total",
			Help: "Total number of errors from database operations.",
		},
		[]string{"operation", "resource_type"},
	)
)
