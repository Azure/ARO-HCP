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

package entrypointutils

import (
	"fmt"
	"strconv"

	"github.com/Azure/ARO-Tools/config"
	configtypes "github.com/Azure/ARO-Tools/config/types"
)

// BuildStampList determines which stamps to process.
// --stamp-count-config-ref wins over --stamp (which may come from dev settings).
func BuildStampList(stamp, stampCountConfigRef string, cfg configtypes.Configuration) ([]string, error) {
	if len(stampCountConfigRef) > 0 {
		rawCount, err := cfg.GetByPath(stampCountConfigRef)
		if err != nil {
			return nil, fmt.Errorf("failed to read stamp count from config path %q: %w", stampCountConfigRef, err)
		}

		var stampCount int
		switch v := rawCount.(type) {
		case int:
			stampCount = v
		case float64:
			if v != float64(int(v)) {
				return nil, fmt.Errorf("stamp count at %q must be an integer, got %v", stampCountConfigRef, v)
			}
			stampCount = int(v)
		default:
			return nil, fmt.Errorf("stamp count at %q is %T, expected int", stampCountConfigRef, rawCount)
		}

		if stampCount < 1 {
			return nil, fmt.Errorf("stamp count at %q must be >= 1, got %d", stampCountConfigRef, stampCount)
		}

		stamps := make([]string, stampCount)
		for i := range stampCount {
			stamps[i] = strconv.Itoa(i + 1)
		}
		return stamps, nil
	}

	return []string{stamp}, nil
}

// ResolveStampConfigs resolves per-stamp configurations by creating a new config
// resolver for each stamp with the stamp baked into the replacements.
func ResolveStampConfigs(stamps []string, provider config.ConfigProvider, baseReplacements config.ConfigReplacements, region string) (map[string]configtypes.Configuration, error) {
	configs := make(map[string]configtypes.Configuration, len(stamps))
	for _, stamp := range stamps {
		replacements := baseReplacements
		replacements.StampReplacement = stamp
		resolver, err := provider.GetResolver(&replacements)
		if err != nil {
			return nil, fmt.Errorf("failed to get resolver for stamp %s: %w", stamp, err)
		}
		stampCfg, err := resolver.GetRegionConfiguration(region)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve config for stamp %s: %w", stamp, err)
		}
		configs[stamp] = stampCfg
	}
	return configs, nil
}
