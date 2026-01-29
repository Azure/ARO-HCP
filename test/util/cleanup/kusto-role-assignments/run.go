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

package kustoroleassignments

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/kusto/armkusto/v2"
)

const (
	resourceGroupName              = "hcp-kusto-us"
	clusterName                    = "hcp-dev-us"
	servicesDatabase               = "ServiceLogs"
	hostedControlPlaneLogsDatabase = "HostedControlPlaneLogs"
	invalidPrincipalName           = "AAD app id failed to be resolved"
)

func (o *Options) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("Deleting Kusto role assignments")

	errGroup, ctx := errgroup.WithContext(ctx)
	errGroup.Go(func() error {
		pager := o.Client.NewListPager(resourceGroupName, clusterName, servicesDatabase, nil)
		return pageAssignmentsAndDeleteInvalid(ctx, pager, o.Client, servicesDatabase)
	})

	errGroup.Go(func() error {
		pager := o.Client.NewListPager(resourceGroupName, clusterName, hostedControlPlaneLogsDatabase, nil)
		return pageAssignmentsAndDeleteInvalid(ctx, pager, o.Client, hostedControlPlaneLogsDatabase)
	})

	return errGroup.Wait()
}

func pageAssignmentsAndDeleteInvalid(ctx context.Context, pager *runtime.Pager[armkusto.DatabasePrincipalAssignmentsClientListResponse], client *armkusto.DatabasePrincipalAssignmentsClient, database string) error {
	logger := logr.FromContextOrDiscard(ctx)
	logger.Info("Deleting role assignments", "database", database)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list role assignments: %w", err)
		}
		for _, roleAssignment := range page.Value {
			if *roleAssignment.Properties.PrincipalName == invalidPrincipalName {
				parts := strings.Split(*roleAssignment.Name, "/")
				if len(parts) != 3 {
					return fmt.Errorf("invalid role assignment name: %s", *roleAssignment.Name)
				}
				name := parts[2]
				logger.Info("Deleting role assignment", "database", database, "name", name)
				poller, err := client.BeginDelete(ctx, resourceGroupName, clusterName, database, name, nil)
				if err != nil {
					return fmt.Errorf("failed to delete role assignment: %w", err)
				}
				var resp any
				if resp, err = poller.PollUntilDone(ctx, nil); err != nil {
					return fmt.Errorf("failed to delete role assignment: %w", err)
				}
				switch m := resp.(type) {
				case armkusto.DatabasePrincipalAssignmentsClientDeleteResponse:
					logger.Info("Role assignment deleted", "database", database, "name", name)
				default:
					logger.Error(fmt.Errorf("unknown type %T", m), "name", name)
				}
			}
		}
	}
	return nil
}
