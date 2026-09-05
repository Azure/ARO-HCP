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

// aks-cluster-create creates a management cluster's AKS ManagedCluster
// resource and all its node pools (system, infra, worker), using the same
// fleet/pkg/compute profiles the nodepool controller uses to plan
// pools on an already-running cluster.
//
// Inputs (environment variables):
//
//	SUBSCRIPTION_ID, RESOURCE_GROUP, CLUSTER_NAME, REGION
//	PROFILE          - nodepool profile name (see fleet/pkg/compute.ValidProfileNames)
//	AZURE_REGION_AVAILABILITY_ZONE_COUNT - number of availability zones the region
//	                   offers; planning fails if zero
//	ZONES            - optional CSV availability zone override, e.g. "1,3" to skip a
//	                   known-bad zone; empty means all of the region's zones
//	                   (1..AZURE_REGION_AVAILABILITY_ZONE_COUNT)
//	NODE_SUBNET_ID, POD_SUBNET_ID     - from the mgmt-infra bicep outputs
//	NETWORK_DATAPLANE, NETWORK_POLICY
//	OUTBOUND_IP_RESOURCE_ID           - from the mgmt-infra bicep outputs
//	MANAGED_IDENTITY_ID               - cluster user-assigned identity resource ID
//	ETCD_KMS_KEY_URI                  - versioned etcd KMS key URI (keyUriWithVersion)
//	KUBERNETES_VERSION
//	CLUSTER_TAGS     - CSV "key=value" list
//	METRIC_LABELS_ALLOWLIST, METRIC_ANNOTATIONS_ALLOWLIST - optional, default ""
//	LOG_VERBOSITY    - optional logr verbosity (default 0)
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/go-logr/logr/funcr"
)

func main() {
	verbosity := 0
	if v := os.Getenv("LOG_VERBOSITY"); len(v) > 0 {
		if n, err := strconv.Atoi(v); err == nil {
			verbosity = n
		}
	}
	logger := funcr.NewJSON(func(obj string) {
		fmt.Fprintln(os.Stderr, obj)
	}, funcr.Options{Verbosity: verbosity})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	raw := newRawOptionsFromEnv(os.Getenv)
	logger = logger.WithValues(
		"subscriptionID", raw.subscriptionID,
		"resourceGroup", raw.resourceGroup,
		"clusterName", raw.clusterName,
		"region", raw.region,
	)

	validated, err := raw.Validate(ctx)
	if err != nil {
		logger.Error(err, "validation failed")
		os.Exit(1)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		logger.Error(err, "completing options failed")
		os.Exit(1)
	}

	if err := run(ctx, completed, logger); err != nil {
		logger.Error(err, "run failed")
		os.Exit(1)
	}
}
