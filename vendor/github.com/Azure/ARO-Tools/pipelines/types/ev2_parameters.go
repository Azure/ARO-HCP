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

package types

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/config"
	configtypes "github.com/Azure/ARO-Tools/config/types"
)

// ReservedEv2HelmStepParameters is the set of every environment variable name that
// sdp-pipelines injects into an EV2 shell extension launch for a Helm step, before any
// values.yaml/namespace-file-derived parameter is added. This budget is consumed from
// EV2's platform-wide shell extension parameter limit (see Ev2ShellExtensionParameterLimit).
// Membership, not order, is the only thing that matters here - sdp-pipelines' own
// tooling is expected to assert its injected names form the identical set (e.g.
// ReservedEv2HelmStepParameters.Equal(sets.New(injectedNames...))), which is exactly
// what a sets.Set gives callers for free, without constructing one themselves first.
//
// Source of truth for injection: Azure/sdp-pipelines tooling/pkg/types/pipeline/shell.go
// (RolloutParameters' system variables) and helm.go (RELEASE_NAME, RELEASE_NAMESPACE,
// EV2_ROLLOUT_VERSION, KUSTO_ENDPOINT). If sdp-pipelines adds, removes, or renames a
// system-injected parameter, this set - and therefore every Helm step's effective
// budget - must be updated in the same change, so that adding a parameter there
// without registering it here fails loudly instead of silently shrinking every Helm
// step's real budget.
//
// Treat this as read-only: nothing in this package defends against a caller mutating
// the shared set.
var ReservedEv2HelmStepParameters = sets.New(
	"zz_injected_AzureCloud",
	"zz_injected_Subscription",
	"zz_injected_ResourceGroup",
	"zz_injected_EV2",
	"AZURE_TOKEN_CREDENTIALS",
	"zz_injected_EV2RolloutVersion",
	"zz_injected_AKSCluster",
	"RELEASE_NAME",
	"RELEASE_NAMESPACE",
	"EV2_ROLLOUT_VERSION",
	"KUSTO_ENDPOINT",
)

// ev2ShellExtensionParameterLimit is EV2's platform-wide cap on the number of
// environment variables a single shell extension launch may declare.
const ev2ShellExtensionParameterLimit = 64

// helmStepEv2ParameterBudget is how many values.yaml/namespace-file-derived parameters
// a Helm step has left once ev2ShellExtensionParameterLimit is reduced by the system
// parameters every Helm step launch carries. It is derived from
// ReservedEv2HelmStepParameters so it cannot drift from that set independently.
var helmStepEv2ParameterBudget = ev2ShellExtensionParameterLimit - len(ReservedEv2HelmStepParameters)

// ValidateEv2Compatibility renders this Helm step's ValuesFile and NamespaceFiles
// with placeholders, then checks their combined EV2 parameter budget.
// configuration supplies the configuration tree used for generation; its leaf
// values are replaced by placeholders. Repeated placeholder names consume one
// parameter across all files, including literal input-variable placeholders.
func (s *HelmStep) ValidateEv2Compatibility(pipelineDir string, configuration configtypes.Configuration) error {
	_, dunderCfg := config.EV2Mapping(configuration, config.NewDunderPlaceholders(), nil)
	names := sets.New[string]()
	var errs []error
	for _, file := range append([]string{s.ValuesFile}, s.NamespaceFiles...) {
		if len(file) == 0 {
			continue
		}
		content, err := os.ReadFile(filepath.Join(pipelineDir, file))
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", file, err)
		}
		_, references, err := config.PrepareEv2Template(content, dunderCfg)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", file, err))
			continue
		}
		for name := range references {
			names.Insert(name)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if len(names) > helmStepEv2ParameterBudget {
		return fmt.Errorf("helm step %q resolves %d templated parameters, exceeding the EV2 shell-extension budget of %d (platform limit %d minus %d reserved for system-injected parameters)",
			s.Name, len(names), helmStepEv2ParameterBudget, ev2ShellExtensionParameterLimit, len(ReservedEv2HelmStepParameters))
	}
	return nil
}
