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

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"time"

	"golang.org/x/time/rate"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utiluuid "k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	tableMediaType         = "application/json;as=Table;g=meta.k8s.io;v=v1"
	resourceInventoryLimit = int64(500)
	resourceInventoryRate  = rate.Limit(100)
	resourceInventoryBurst = 20
	maxScheduleJitter      = 5 * time.Second
)

var defaultResourceInventoryTargets = []resourceInventoryTarget{
	{
		gvr:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
		kind:     "Pod",
		scope:    "Namespaced",
		interval: 5 * time.Minute,
	},
	{
		gvr:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"},
		kind:     "Secret",
		scope:    "Namespaced",
		interval: 5 * time.Minute,
	},
	{
		gvr:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"},
		kind:     "Namespace",
		scope:    "Cluster",
		interval: 10 * time.Minute,
	},
}

type resourceInventoryTarget struct {
	gvr      schema.GroupVersionResource
	kind     string
	scope    string
	interval time.Duration
}

type resourceInventoryTableLister interface {
	ListTablePage(ctx context.Context, gvr schema.GroupVersionResource, limit int64, continueToken string) ([]byte, error)
}

type coreV1TableLister struct {
	client rest.Interface
}

func (l *coreV1TableLister) ListTablePage(ctx context.Context, gvr schema.GroupVersionResource, limit int64, continueToken string) ([]byte, error) {
	if gvr.Group != "" || gvr.Version != "v1" {
		return nil, fmt.Errorf("unsupported inventory resource %s: only core/v1 is supported", gvr.String())
	}

	req := l.client.Get().
		Resource(gvr.Resource).
		Param("limit", fmt.Sprintf("%d", limit)).
		Param("includeObject", string(metav1.IncludeMetadata)).
		SetHeader("Accept", tableMediaType)
	if continueToken != "" {
		req.Param("continue", continueToken)
	}

	return req.Do(ctx).Raw()
}

type resourceInventoryItem struct {
	SnapshotID   string
	SnapshotTime time.Time
	Target       resourceInventoryTarget
	Namespace    string
	Name         string
	ItemIndex    int64
	Cells        []interface{}
}

type resourceInventorySummary struct {
	SnapshotID                string
	SnapshotTime              time.Time
	Target                    resourceInventoryTarget
	Status                    string
	ExpectedItemCount         int64
	ListResourceVersion       string
	ListStartedAt             time.Time
	ListCompletedAt           time.Time
	ListDuration              time.Duration
	EmitStartedAt             time.Time
	EmitCompletedAt           time.Time
	EmitDuration              time.Duration
	ScheduledAt               time.Time
	ScheduleDelay             time.Duration
	NextCheckpointAt          time.Time
	PageCount                 int64
	UncompressedResponseBytes int64
	ErrorMessage              string
	PrinterColumns            []metav1.TableColumnDefinition
}

type resourceInventorySchedule struct {
	target resourceInventoryTarget
	next   time.Time
}

// ResourceInventory periodically captures the server-side Kubernetes Table
// representation of selected resources. It deliberately persists only the
// normal/wide printer cells; the underlying objects and their metadata are
// discarded after extracting the namespace needed for all-namespace output.
type ResourceInventory struct {
	lister      resourceInventoryTableLister
	targets     []resourceInventoryTarget
	limiter     *rate.Limiter
	now         func() time.Time
	jitter      func(time.Duration) time.Duration
	emitItem    func(resourceInventoryItem)
	emitSummary func(resourceInventorySummary)
}

func NewResourceInventory(client rest.Interface) *ResourceInventory {
	return &ResourceInventory{
		lister:  &coreV1TableLister{client: client},
		targets: append([]resourceInventoryTarget(nil), defaultResourceInventoryTargets...),
		limiter: rate.NewLimiter(resourceInventoryRate, resourceInventoryBurst),
		now:     time.Now,
		jitter: func(max time.Duration) time.Duration {
			if max <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(2*max)+1)) - max
		},
		emitItem:    logResourceInventoryItem,
		emitSummary: logResourceInventorySummary,
	}
}

func (c *ResourceInventory) Run(ctx context.Context) error {
	logger := klog.FromContext(ctx)
	logger.Info("Starting resource inventory", "targets", len(c.targets), "pageSize", resourceInventoryLimit,
		"recordsPerSecond", resourceInventoryRate, "burst", resourceInventoryBurst)

	schedules := c.initialSchedules(c.now())
	for {
		sort.Slice(schedules, func(i, j int) bool { return schedules[i].next.Before(schedules[j].next) })
		next := &schedules[0]
		if err := waitUntil(ctx, c.now, next.next); err != nil {
			logger.Info("Shutting down resource inventory")
			return nil
		}

		scheduledAt := next.next
		snapshotTime := c.now().UTC()
		next.next = snapshotTime.Add(next.target.interval + c.jitter(maxScheduleJitter))
		c.capture(ctx, next.target, scheduledAt, snapshotTime, next.next)
	}
}

func (c *ResourceInventory) initialSchedules(start time.Time) []resourceInventorySchedule {
	schedules := make([]resourceInventorySchedule, 0, len(c.targets))
	minimumInterval := c.targets[0].interval
	for _, target := range c.targets[1:] {
		if target.interval < minimumInterval {
			minimumInterval = target.interval
		}
	}
	spacing := minimumInterval / time.Duration(len(c.targets))
	for i, target := range c.targets {
		delay := time.Duration(i)*spacing + c.jitter(maxScheduleJitter)
		if delay < 0 {
			delay = 0
		}
		schedules = append(schedules, resourceInventorySchedule{target: target, next: start.Add(delay)})
	}
	return schedules
}

func waitUntil(ctx context.Context, now func() time.Time, deadline time.Time) error {
	delay := deadline.Sub(now())
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *ResourceInventory) capture(ctx context.Context, target resourceInventoryTarget, scheduledAt, snapshotTime, nextCheckpointAt time.Time) {
	summary := resourceInventorySummary{
		SnapshotID:       string(utiluuid.NewUUID()),
		SnapshotTime:     snapshotTime,
		Target:           target,
		Status:           "Failed",
		ListStartedAt:    snapshotTime,
		ScheduledAt:      scheduledAt.UTC(),
		ScheduleDelay:    maxDuration(0, snapshotTime.Sub(scheduledAt)),
		NextCheckpointAt: nextCheckpointAt.UTC(),
	}

	items, err := c.collect(ctx, &summary)
	summary.ListCompletedAt = c.now().UTC()
	summary.ListDuration = summary.ListCompletedAt.Sub(summary.ListStartedAt)
	if err != nil {
		summary.ErrorMessage = err.Error()
		c.emitSummary(summary)
		return
	}

	summary.ExpectedItemCount = int64(len(items))
	summary.EmitStartedAt = c.now().UTC()
	for i := range items {
		if err := c.limiter.Wait(ctx); err != nil {
			summary.EmitCompletedAt = c.now().UTC()
			summary.EmitDuration = summary.EmitCompletedAt.Sub(summary.EmitStartedAt)
			summary.ErrorMessage = err.Error()
			c.emitSummary(summary)
			return
		}
		c.emitItem(items[i])
	}
	summary.EmitCompletedAt = c.now().UTC()
	summary.EmitDuration = summary.EmitCompletedAt.Sub(summary.EmitStartedAt)
	summary.Status = "Complete"
	c.emitSummary(summary)
}

func (c *ResourceInventory) collect(ctx context.Context, summary *resourceInventorySummary) ([]resourceInventoryItem, error) {
	var items []resourceInventoryItem
	var expectedColumns []metav1.TableColumnDefinition
	continueToken := ""

	for {
		raw, err := c.lister.ListTablePage(ctx, summary.Target.gvr, resourceInventoryLimit, continueToken)
		if err != nil {
			return nil, fmt.Errorf("list %s table page: %w", summary.Target.gvr.Resource, err)
		}
		summary.PageCount++
		summary.UncompressedResponseBytes += int64(len(raw))

		var table metav1.Table
		if err := json.Unmarshal(raw, &table); err != nil {
			return nil, fmt.Errorf("decode %s table page: %w", summary.Target.gvr.Resource, err)
		}
		if table.APIVersion != metav1.SchemeGroupVersion.String() || table.Kind != "Table" {
			return nil, fmt.Errorf("%s returned %s %s instead of meta.k8s.io/v1 Table", summary.Target.gvr.Resource, table.APIVersion, table.Kind)
		}
		if expectedColumns == nil {
			expectedColumns = append([]metav1.TableColumnDefinition(nil), table.ColumnDefinitions...)
			summary.PrinterColumns = expectedColumns
		} else if !reflect.DeepEqual(expectedColumns, table.ColumnDefinitions) {
			return nil, fmt.Errorf("%s table columns changed between pages", summary.Target.gvr.Resource)
		}

		if tableNameColumn(table.ColumnDefinitions) < 0 {
			return nil, fmt.Errorf("%s table has no name-formatted column", summary.Target.gvr.Resource)
		}
		for _, row := range table.Rows {
			if len(row.Cells) != len(table.ColumnDefinitions) {
				return nil, fmt.Errorf("%s table row has %d cells for %d columns", summary.Target.gvr.Resource, len(row.Cells), len(table.ColumnDefinitions))
			}
			namespace, name, err := tableRowIdentity(row)
			if err != nil {
				return nil, fmt.Errorf("decode %s table row metadata: %w", summary.Target.gvr.Resource, err)
			}
			if summary.Target.scope == "Namespaced" && namespace == "" {
				return nil, fmt.Errorf("%s table row %q has no namespace", summary.Target.gvr.Resource, name)
			}
			items = append(items, resourceInventoryItem{
				SnapshotID:   summary.SnapshotID,
				SnapshotTime: summary.SnapshotTime,
				Target:       summary.Target,
				Namespace:    namespace,
				Name:         name,
				ItemIndex:    int64(len(items)),
				Cells:        append([]interface{}(nil), row.Cells...),
			})
		}

		if summary.ListResourceVersion == "" {
			summary.ListResourceVersion = table.ResourceVersion
		} else if table.ResourceVersion != "" && summary.ListResourceVersion != table.ResourceVersion {
			return nil, fmt.Errorf("%s resourceVersion changed between pages", summary.Target.gvr.Resource)
		}
		continueToken = table.Continue
		if continueToken == "" {
			break
		}
	}

	return items, nil
}

func tableNameColumn(columns []metav1.TableColumnDefinition) int {
	for i, column := range columns {
		if column.Format == "name" && column.Type == "string" {
			return i
		}
	}
	return -1
}

func tableRowIdentity(row metav1.TableRow) (string, string, error) {
	if len(row.Object.Raw) == 0 {
		return "", "", fmt.Errorf("has no included object metadata")
	}
	var metadata metav1.PartialObjectMetadata
	if err := json.Unmarshal(row.Object.Raw, &metadata); err != nil {
		return "", "", err
	}
	if metadata.APIVersion != metav1.SchemeGroupVersion.String() || metadata.Kind != "PartialObjectMetadata" {
		return "", "", fmt.Errorf("got %s %s instead of meta.k8s.io/v1 PartialObjectMetadata", metadata.APIVersion, metadata.Kind)
	}
	if metadata.Name == "" {
		return "", "", fmt.Errorf("has no object name")
	}
	return metadata.Namespace, metadata.Name, nil
}

func logResourceInventoryItem(item resourceInventoryItem) {
	klog.InfoS("resource inventory item",
		"snapshotType", "kubernetes-inventory-item",
		"snapshotId", item.SnapshotID,
		"snapshotTime", item.SnapshotTime,
		"apiGroup", item.Target.gvr.Group,
		"apiVersion", item.Target.gvr.Version,
		"resource", item.Target.gvr.Resource,
		"objectKind", item.Target.kind,
		"scope", item.Target.scope,
		"namespace", item.Namespace,
		"name", item.Name,
		"itemIndex", item.ItemIndex,
		"display", item.Cells,
	)
}

func logResourceInventorySummary(summary resourceInventorySummary) {
	klog.InfoS("resource inventory snapshot",
		"snapshotType", "kubernetes-inventory-snapshot",
		"snapshotId", summary.SnapshotID,
		"snapshotTime", summary.SnapshotTime,
		"apiGroup", summary.Target.gvr.Group,
		"apiVersion", summary.Target.gvr.Version,
		"resource", summary.Target.gvr.Resource,
		"objectKind", summary.Target.kind,
		"scope", summary.Target.scope,
		"status", summary.Status,
		"expectedItemCount", summary.ExpectedItemCount,
		"listResourceVersion", summary.ListResourceVersion,
		"listStartedAt", summary.ListStartedAt,
		"listCompletedAt", summary.ListCompletedAt,
		"listDurationMs", summary.ListDuration.Milliseconds(),
		"emitStartedAt", nullableTime(summary.EmitStartedAt),
		"emitCompletedAt", nullableTime(summary.EmitCompletedAt),
		"emitDurationMs", summary.EmitDuration.Milliseconds(),
		"scheduledAt", summary.ScheduledAt,
		"scheduleDelayMs", summary.ScheduleDelay.Milliseconds(),
		"nextCheckpointAt", summary.NextCheckpointAt,
		"pageCount", summary.PageCount,
		"uncompressedResponseBytes", summary.UncompressedResponseBytes,
		"errorMessage", summary.ErrorMessage,
		"printerColumns", summary.PrinterColumns,
	)
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func nullableTime(value time.Time) interface{} {
	if value.IsZero() {
		return nil
	}
	return value
}
