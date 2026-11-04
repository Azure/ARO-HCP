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
	"time"
)

// This file defines small internal domain types that model the subset of Grafana
// data grafanactl actually uses. They insulate the rest of the tool from the
// upstream Grafana SDK so the HTTP/client layer can be swapped (see ARO-29066)
// without churning consumers. The client layer is responsible for converting
// between these types and whatever SDK it wraps.

// Folder is a Grafana folder. Folders are identified by UID; Grafana's numeric
// folder IDs are deprecated and intentionally not modeled here.
type Folder struct {
	UID   string
	Title string
}

// FoundBoard is a single result from Grafana's search API. It represents either
// a dashboard or a folder, distinguished by Type ("dash-db" or "dash-folder").
type FoundBoard struct {
	UID       string
	Title     string
	Type      string
	FolderUID string
}

// Datasource is a Grafana datasource. Only the fields grafanactl surfaces are
// modeled; JSON tags mirror the Grafana API so `list --output json` output is
// unchanged.
type Datasource struct {
	ID   uint   `json:"id"`
	UID  string `json:"uid"`
	Name string `json:"name"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

// BoardProperties holds the dashboard metadata Grafana returns alongside a
// dashboard body (the "meta" envelope).
type BoardProperties struct {
	Created   time.Time
	FolderUID string
}

// PermissionType mirrors Grafana's numeric folder permission levels.
type PermissionType uint

const (
	PermissionView  = PermissionType(1)
	PermissionEdit  = PermissionType(2)
	PermissionAdmin = PermissionType(4)
)

// FolderPermission is a single entry in a folder's access-control list.
type FolderPermission struct {
	Role       string
	Permission PermissionType
}

// dashboardMetadata is the handful of fields grafanactl validates from a
// dashboard's JSON. Everything else is intentionally ignored here; the full,
// lossless dashboard body is carried separately as raw JSON.
type dashboardMetadata struct {
	Title      string `json:"title"`
	UID        string `json:"uid"`
	Templating struct {
		List []templateVar `json:"list"`
	} `json:"templating"`
}

// templateVar is a dashboard templating variable, restricted to the fields used
// during validation. Query is untyped because Grafana represents it as either a
// string (e.g. "prometheus") or an object depending on the variable type.
type templateVar struct {
	Type  string      `json:"type"`
	Query interface{} `json:"query"`
	Regex string      `json:"regex"`
}
