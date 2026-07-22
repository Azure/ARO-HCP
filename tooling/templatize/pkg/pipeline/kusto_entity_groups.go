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

package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/kustoctl/cmd/entitygroups"
)

func runKustoEntityGroupsStep(_ graph.Identifier, step *types.KustoEntityGroupsStep, ctx context.Context) error {
	opts := entitygroups.DefaultSyncOptions()
	opts.EntityGroups = step.EntityGroups

	if step.Timeout != "" {
		d, err := time.ParseDuration(step.Timeout)
		if err != nil {
			return fmt.Errorf("failed to parse timeout %q: %w", step.Timeout, err)
		}
		opts.Timeout = d
	}

	return opts.Run(ctx)
}
