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

package config

import (
	"regexp"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/config/types"
)

var dunderReferenceMatcher = regexp.MustCompile(`__([a-zA-Z0-9.]+)__`)

// ExtractDunderReferences returns distinct placeholder names from rendered
// content, including literal input placeholders. Names contain only ASCII
// letters, digits and periods, matching sdp-pipelines' placeholder extraction.
// Callers must combine references across all files belonging to a Helm step.
func ExtractDunderReferences(content []byte) sets.Set[string] {
	names := sets.New[string]()
	for _, match := range dunderReferenceMatcher.FindAllSubmatch(content, -1) {
		names.Insert(string(match[1]))
	}
	return names
}

// PrepareEv2Template renders with an already placeholder-shaped configuration
// and returns the rendered bytes and their distinct placeholder names,
// including literal input placeholders.
// Callers must union names across files and sort them for deterministic output.
func PrepareEv2Template(content []byte, dunderCfg types.Configuration) ([]byte, sets.Set[string], error) {
	rendered, err := PreprocessContent(content, dunderCfg)
	if err != nil {
		return nil, nil, err
	}
	return rendered, ExtractDunderReferences(rendered), nil
}
