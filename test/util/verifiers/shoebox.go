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

package verifiers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

// insightsContainerPrefix is the prefix Azure Monitor uses when it creates one blob
// container per enabled diagnostic log category: insights-logs-<category>.
const insightsContainerPrefix = "insights-logs-"

type verifyShoeboxLogsImpl struct {
	client             *armstorage.BlobContainersClient
	resourceGroupName  string
	storageAccountName string
}

func (v verifyShoeboxLogsImpl) Name() string {
	return "VerifyShoeboxLogs"
}

type retryableError struct {
	err error
}

func (e *retryableError) Error() string {
	return e.err.Error()
}

func (e *retryableError) Unwrap() error {
	return e.err
}

// IsRetryable reports whether err is a retryable error.
func IsRetryable(err error) bool {
	var re *retryableError
	return errors.As(err, &re)
}

func (v verifyShoeboxLogsImpl) Verify(ctx context.Context) error {
	containers, err := v.listInsightsContainers(ctx)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return &retryableError{err: fmt.Errorf("expected at least one insights-logs-* container in storage account %s, got none", v.storageAccountName)}
	}
	return nil
}

// ObservedCategories returns the shoebox log categories for which Azure Monitor has
// created an insights-logs-<category> blob container in the storage account, sorted. A
// container is only created once at least one log record of that category has been
// delivered, so its presence is a reliable per-category signal.
func (v verifyShoeboxLogsImpl) ObservedCategories(ctx context.Context) ([]string, error) {
	containers, err := v.listInsightsContainers(ctx)
	if err != nil {
		return nil, err
	}

	categories := make([]string, 0, len(containers))
	for _, container := range containers {
		categories = append(categories, strings.TrimPrefix(container, insightsContainerPrefix))
	}
	sort.Strings(categories)
	return categories, nil
}

func (v verifyShoeboxLogsImpl) listInsightsContainers(ctx context.Context) ([]string, error) {
	var controlPlaneContainers []string
	pager := v.client.NewListPager(v.resourceGroupName, v.storageAccountName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list blob containers: %w", err)
		}
		for _, container := range page.Value {
			if container.Name != nil && strings.HasPrefix(*container.Name, insightsContainerPrefix) {
				controlPlaneContainers = append(controlPlaneContainers, *container.Name)
			}
		}
	}
	return controlPlaneContainers, nil
}

// VerifyShoeboxLogs creates a verifier that checks for the presence of
// insights-logs-* blob containers in the given storage account, which
// indicates that Azure Monitor diagnostic logs are being forwarded.
func VerifyShoeboxLogs(client *armstorage.BlobContainersClient, resourceGroupName, storageAccountName string) verifyShoeboxLogsImpl {
	return verifyShoeboxLogsImpl{
		client:             client,
		resourceGroupName:  resourceGroupName,
		storageAccountName: storageAccountName,
	}
}
