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

package appregistrations

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	graphutil "github.com/Azure/ARO-HCP/internal/graph/util"
)

func (o *Options) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("Listing owned expired app registrations")
	expiredApps, err := o.GraphClient.ListOwnedExpiredApplications(ctx)
	if err != nil {
		return fmt.Errorf("failed to list owned expired app registrations: %w", err)
	}

	if len(expiredApps) == 0 {
		logger.Info("No expired app registrations found")
		return nil
	}

	targets := make([]graphutil.ApplicationCleanupTarget, 0, len(expiredApps))
	for _, app := range expiredApps {
		servicePrincipal, err := o.GraphClient.GetServicePrincipalByAppID(ctx, app.AppID)
		if err != nil {
			return fmt.Errorf("failed to resolve service principal for app registration %q: %w", app.ID, err)
		}

		target := graphutil.ApplicationCleanupTarget{
			ApplicationObjectID: app.ID,
		}
		if servicePrincipal != nil {
			target.ServicePrincipalObjectID = servicePrincipal.ID
		}
		targets = append(targets, target)

		logger.Info(
			"Found expired app registration",
			"clientID", app.AppID,
			"applicationObjectID", app.ID,
			"servicePrincipalObjectID", target.ServicePrincipalObjectID,
			"displayName", app.DisplayName,
		)
	}

	if o.DryRun {
		logger.Info("Dry run, not deleting", "count", len(expiredApps))
		return nil
	}

	logger.Info("Deleting and purging owned expired app registrations", "count", len(targets))
	if err := o.GraphClient.CleanupApplications(ctx, targets); err != nil {
		return fmt.Errorf("failed to delete and purge app registrations: %w", err)
	}

	logger.Info("All expired app registrations and service principals successfully purged", "count", len(targets))
	return nil
}
