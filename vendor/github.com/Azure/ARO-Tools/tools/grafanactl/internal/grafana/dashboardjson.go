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

package grafana

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// PrepareDashboardForUpload rewrites a dashboard document so that Grafana
// stores it as a new dashboard, or replaces the dashboard that already has the
// given UID.
//
// The document keeps every field it came with: it is decoded into a map with
// json.Number, so fields this tool knows nothing about (plugin options, panel
// internals) survive, and large integers do not go through float64.
//
// A document wrapped as {"dashboard": {...}} is unwrapped first, which is the
// shape Grafana's export produces. A non-empty uid or title replaces the one in
// the document. "id" is set to null and "version" to 0, which tells Grafana to
// key the dashboard off its UID instead of the numeric ID and version the
// source file was exported with.
func PrepareDashboardForUpload(raw []byte, uid, title string) ([]byte, error) {
	dashboard, err := decodeDashboardObject(raw)
	if err != nil {
		return nil, err
	}

	if inner, ok := dashboard["dashboard"]; ok {
		nested, ok := inner.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("dashboard: \"dashboard\" field is not a JSON object")
		}
		dashboard = nested
	}

	if uid != "" {
		dashboard["uid"] = uid
	}
	if title != "" {
		dashboard["title"] = title
	}
	dashboard["id"] = nil
	dashboard["version"] = json.Number("0")

	prepared, err := json.Marshal(dashboard)
	if err != nil {
		return nil, fmt.Errorf("failed to encode dashboard: %w", err)
	}
	return prepared, nil
}

// GetDashboardStringField reads a top-level string field from a dashboard
// document. It reports an empty string when the field is absent or is not a
// string. Callers use it to recover the effective uid and title after
// PrepareDashboardForUpload has applied its overrides.
func GetDashboardStringField(raw []byte, field string) (string, error) {
	dashboard, err := decodeDashboardObject(raw)
	if err != nil {
		return "", err
	}

	value, ok := dashboard[field].(string)
	if !ok {
		return "", nil
	}
	return value, nil
}

// decodeDashboardObject decodes exactly one JSON object, preserving numbers verbatim.
func decodeDashboardObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var dashboard map[string]any
	if err := decoder.Decode(&dashboard); err != nil {
		return nil, fmt.Errorf("failed to parse dashboard JSON: %w", err)
	}
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("dashboard contains multiple JSON values")
		}
		return nil, fmt.Errorf("dashboard contains trailing data: %w", err)
	}

	return dashboard, nil
}
