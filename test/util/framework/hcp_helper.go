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

package framework

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/davecgh/go-spew/spew"
	"golang.org/x/sync/errgroup"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	hcpapi20240610 "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

// DeleteHCPCluster deletes an hcp cluster and waits for the operation to complete
func DeleteHCPCluster(
	ctx context.Context,
	hcpClient *hcpapi20240610.HcpOpenShiftClustersClient,
	resourceGroupName string,
	hcpClusterName string,
	interval time.Duration,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	poller, err := hcpClient.BeginDelete(ctx, resourceGroupName, hcpClusterName, nil)
	if err != nil {
		return err
	}

	operationResult, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: interval,
	})
	if err != nil {
		return fmt.Errorf("failed waiting for hcpCluster=%q in resourcegroup=%q to finish deleting: %w", hcpClusterName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case hcpapi20240610.HcpOpenShiftClustersClientDeleteResponse:
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return fmt.Errorf("unknown type %T", m)
	}

	return nil
}

// DeleteResourceGroup deletes a resource group and waits for the operation to complete
func DeleteAllHCPClusters(
	ctx context.Context,
	hcpClient *hcpapi20240610.HcpOpenShiftClustersClient,
	resourceGroupName string,
	interval time.Duration,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	hcpClusterNames := []string{}
	hcpClusterPager := hcpClient.NewListByResourceGroupPager(resourceGroupName, nil)
	for hcpClusterPager.More() {
		page, err := hcpClusterPager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed listing hcp clusters in resourcegroup=%q: %w", resourceGroupName, err)
		}
		for _, sub := range page.Value {
			hcpClusterNames = append(hcpClusterNames, *sub.Name)
		}
	}

	// deletion takes a while, it's worth it to do this in parallel
	waitGroup, ctx := errgroup.WithContext(ctx)
	for _, hcpClusterName := range hcpClusterNames {
		// https://golang.org/doc/faq#closures_and_goroutines
		hcpClusterName := hcpClusterName
		waitGroup.Go(func() error {
			// prevent a stray panic from exiting the process. Don't do this generally because ginkgo/gomega rely on panics ot funcion.
			utilruntime.HandleCrashWithContext(ctx)

			return DeleteHCPCluster(ctx, hcpClient, resourceGroupName, hcpClusterName, interval, timeout)
		})
	}
	if err := waitGroup.Wait(); err != nil {
		// remember that Wait only shows the first error, not all the errors.
		return fmt.Errorf("at least one hcp cluster failed to delete: %w", err)
	}

	return nil
}
