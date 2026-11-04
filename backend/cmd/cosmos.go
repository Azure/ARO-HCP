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

package cmd

import (
	"context"
	"fmt"
	"math"

	"github.com/Azure/ARO-HCP/backend/pkg/app"
)

type containerThroughputReader interface {
	ContainerThroughput(context.Context, string) (float64, error)
}

func discoverStorageOptions(ctx context.Context, account containerThroughputReader, controllerContext app.ControllerContext) (app.StorageFactoryOptions, error) {
	var resourcesRUsPerSecond, billingRUsPerSecond, fleetRUsPerSecond float64
	for _, container := range []struct {
		name  string
		value *float64
	}{
		{"Resources", &resourcesRUsPerSecond},
		{"Billing", &billingRUsPerSecond},
		{"Fleet", &fleetRUsPerSecond},
	} {
		throughput, err := account.ContainerThroughput(ctx, container.name)
		if err != nil {
			return app.StorageFactoryOptions{}, fmt.Errorf("discover %s throughput: %w", container.name, err)
		}
		if throughput <= 0 || math.IsNaN(throughput) || math.IsInf(throughput, 0) {
			return app.StorageFactoryOptions{}, fmt.Errorf("discover %s throughput: expected positive finite RU/s, got %v", container.name, throughput)
		}
		*container.value = throughput
	}
	return app.BackendStorageFactoryOptions(controllerContext, resourcesRUsPerSecond, billingRUsPerSecond, fleetRUsPerSecond), nil
}
