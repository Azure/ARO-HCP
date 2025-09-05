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

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func NewDeleteExpiredResourcesCommand() *cobra.Command {
	nowString := time.Now().Format(time.RFC3339)
	dryRun := false

	cmd := &cobra.Command{
		Use:          "delete-expired-resources",
		Short:        "Look for all expired resources from e2e runs that need to be deleted.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancelCause := context.WithCancelCause(context.Background())
			defer cancelCause(errors.New("exiting"))

			abortCh := make(chan os.Signal, 2)
			go func() {
				<-abortCh
				fmt.Fprintf(os.Stderr, "Interrupted, terminating tests")
				cancelCause(errors.New("interrupt received"))

				select {
				case sig := <-abortCh:
					fmt.Fprintf(os.Stderr, "Interrupted twice, exiting (%s)", sig)
					switch sig {
					case syscall.SIGINT:
						os.Exit(130)
					default:
						os.Exit(130) // if we were interrupted, never return zero.
					}

				case <-time.After(30 * time.Minute): // allow time for cleanup.  If we finish before this, we'll exit
					fmt.Fprintf(os.Stderr, "Timed out during cleanup, exiting")
					os.Exit(130) // if we were interrupted, never return zero.
				}
			}()
			signal.Notify(abortCh, syscall.SIGINT, syscall.SIGTERM)

			now, err := time.Parse(time.RFC3339, nowString)
			if err != nil {
				return err
			}

			// convenient way to get clients
			tc := framework.NewTestContext()
			expiredResourceGroups, err := framework.ListAllExpiredResourceGroups(
				ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient(),
				now,
			)
			if err != nil {
				return fmt.Errorf("failed to list expired resource groups: %w", err)
			}

			expiredResourceGroupsNames := []string{}
			for _, resourceGroup := range expiredResourceGroups {
				fmt.Printf("Deleting resource group %s\n", *resourceGroup.Name)
				expiredResourceGroupsNames = append(expiredResourceGroupsNames, *resourceGroup.Name)
			}
			if dryRun {
				return nil
			}

			err = framework.CleanupResourceGroups(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient(),
				expiredResourceGroupsNames)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error during cleanup: %v", err)
			}

			// App registration cleanup
			graphClient := tc.GetGraphClientOrDie(ctx)
			expiredAppRegistrations, err := graphClient.ListAllExpiredApplications(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error listing expired app registrations: %v", err)
			}

			//TODO (bvesel) - need to ensure we're owner over the app registrations otherwise they won't delete
			appObjectIds := []string{}
			for _, app := range expiredAppRegistrations {
				fmt.Printf("Deleting app registration ClientID=%s ObjectID=%s\n", app.AppID, app.ID)
				appObjectIds = append(appObjectIds, app.ID)
			}

			err = framework.CleanupAppRegistrations(ctx,
				graphClient,
				appObjectIds)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error during app registration cleanup: %v", err)
			}

			return err
		},
	}

	cmd.Flags().StringVar(&nowString, "now", nowString, "The current time")
	cmd.Flags().BoolVar(&dryRun, "dry-run", dryRun, "Print what would be deleted, but don't actually delete anything")

	return cmd
}
