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

package gatherobservability

import (
	"encoding/json"
	"html/template"

	_ "embed"
)

//go:embed artifacts/alert-diagnostics.js
var alertDiagnosticsJavaScript string

func alertDiagnosticsScript() template.JS {
	return template.JS(alertDiagnosticsJavaScript) //nolint:gosec // Static embedded source, never report data.
}

// alertDiagnosticsJSON accepts the collector's report pointer. Keep the rendering
// boundary independent of the collector's Go representation; the browser consumes
// its versioned JSON contract.
func alertDiagnosticsJSON(report any) template.JS {
	data, err := json.Marshal(report)
	if err != nil {
		data, _ = json.Marshal(map[string]string{"error": "Cannot serialize alert diagnostics: " + err.Error()})
	}
	// The default HTML escaping also protects </script> and Unicode separators.
	return template.JS(data) //nolint:gosec // Only HTML-escaped json.Marshal output is trusted.
}
