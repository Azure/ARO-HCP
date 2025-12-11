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

package controllers

import (
	"context"
	"log/slog"
	"path"

	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

type Controller interface {
	Run(ctx context.Context, threadiness int)
}

// hcpClusterKey is for driving workqueues keyed for clusters
type hcpClusterKey struct {
	subscriptionID    string
	resourceGroupName string
	hcpClusterName    string
}

func (k *hcpClusterKey) getResourceID() *azcorearm.ResourceID {
	parts := []string{
		"/subscriptions",
		k.subscriptionID,
		"resourceGroups",
		k.resourceGroupName,
		"providers",
		api.ProviderNamespace,
		api.ClusterResourceType.Type,
		k.hcpClusterName,
	}

	return api.Must(azcorearm.ParseResourceID(path.Join(parts...)))
}

func (k *hcpClusterKey) addLoggerValues(logger *slog.Logger) {
	logger.With(
		"subscription_id", k.subscriptionID,
		"resource_group", k.resourceGroupName,
		"resource_name", k.hcpClusterName,
		"resource_id", k.getResourceID().String(),
		"hcp_cluster_name", k.hcpClusterName, // provides standard location for resources like nodes
	)
}

var clock utilsclock.Clock = utilsclock.RealClock{}

func setCondition(conditions *[]api.Condition, toSet api.Condition) {
	existingCondition := getCondition(*conditions, toSet.Type)
	if existingCondition == nil {
		toSet.LastTransitionTime = clock.Now()
		*conditions = append(*conditions, toSet)
		return
	}

	if existingCondition.Status != toSet.Status {
		existingCondition.LastTransitionTime = clock.Now()
	}
	existingCondition.Status = toSet.Status
	existingCondition.Reason = toSet.Reason
	existingCondition.Message = toSet.Message
}

func getCondition(conditions []api.Condition, conditionType string) *api.Condition {
	if conditions == nil {
		return nil
	}
	for _, currentCondition := range conditions {
		if currentCondition.Type == conditionType {
			return &currentCondition
		}
	}

	return nil
}
