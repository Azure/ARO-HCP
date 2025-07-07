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

package mc

import (
	"context"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/cmd/base"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/aks"
)

// CompleteMCList performs discovery client initialization to create fully usable list options.
func CompleteMCList(ctx context.Context, validated *base.ValidatedListAKSOptions) (*base.CompleteListAKSOptions, error) {
	// Create management cluster filter based on validated region
	filter := aks.NewMgmtClusterFilter(validated.Region, "")
	return validated.CompleteWithFilter(ctx, filter)
}

// CompleteBreakglassMC performs discovery and initialization
func CompleteBreakglassMC(ctx context.Context, validated *base.ValidatedBreakglassAKSOptions) (*base.CompletedBreakglassAKSOptions, error) {
	return validated.Complete(ctx, aks.ManagementClusterType)
}
