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

// Package kubeget implements kubectl-like resource reads backed by Kusto.
package kubeget

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ServiceLogsDatabase = "ServiceLogs"

type Snapshot struct {
	ID                string
	Time              time.Time
	APIGroup          string
	APIVersion        string
	Resource          string
	ObjectKind        string
	Scope             string
	ExpectedItemCount int64
	PrinterColumns    []metav1.TableColumnDefinition
}

func (s Snapshot) Namespaced() bool {
	return s.Scope == "Namespaced"
}

type Item struct {
	Snapshot  Snapshot
	Namespace string
	Name      string
	Index     int64
	Display   []any
}

type Detail struct {
	APIVersion string
	ObjectKind string
	Namespace  string
	Name       string
	Timestamp  time.Time
	Event      string
	Object     map[string]any
}

type ItemFilter struct {
	Namespace     string
	AllNamespaces bool
	Name          string
}

type Repository interface {
	LatestSnapshots(ctx context.Context, cluster string) ([]Snapshot, error)
	Items(ctx context.Context, cluster string, snapshots []Snapshot, filter ItemFilter) ([]Item, error)
	Details(ctx context.Context, cluster string, items []Item) ([]Detail, error)
}
