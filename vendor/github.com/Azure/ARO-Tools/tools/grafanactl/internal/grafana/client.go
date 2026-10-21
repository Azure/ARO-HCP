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

package grafana

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-openapi/strfmt"
	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/dashboards"
	"github.com/grafana/grafana-openapi-client-go/client/datasources"
	"github.com/grafana/grafana-openapi-client-go/client/folders"
	"github.com/grafana/grafana-openapi-client-go/client/search"
	"github.com/grafana/grafana-openapi-client-go/models"
	gtransport "github.com/grafana/grafana-openapi-client-go/pkg/transport"

	"k8s.io/utils/set"

	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/azure"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	// grafanaAPITimeout bounds a single Grafana API call, preserving the
	// per-request timeout previously enforced by the HTTP client.
	grafanaAPITimeout = 60 * time.Second
	// grafanaAPIRetryMax and grafanaAPIRetryTimeout configure the openapi
	// transport's native retry behavior (replacing hashicorp/go-retryablehttp).
	grafanaAPIRetryMax     = 5
	grafanaAPIRetryTimeout = 2 * time.Second
	// grafanaDashboardMaxBytes caps a dashboard GET so a runaway response
	// cannot OOM the process. Managed dashboards are well below this.
	grafanaDashboardMaxBytes int64 = 8 << 20
)

// grafanaAPIRetryStatusCodes lists the HTTP status codes the transport retries.
// "5xx" is a single-digit wildcard understood by the transport config.
var grafanaAPIRetryStatusCodes = []string{"429", "5xx"}

// grafanaAPILocation is the scheme/host/base-path triplet the Grafana HTTP API
// is reached at. BasePath includes any reverse-proxy prefix plus "/api".
type grafanaAPILocation struct {
	Scheme   string
	Host     string
	BasePath string
}

// Client provides methods to interact with Azure Managed Grafana instances.
type Client struct {
	grafanaClient *goapi.GrafanaHTTPAPI
	httpClient    *http.Client
	apiBase       *url.URL
	token         string
}

// NewClient creates a new authenticated Grafana client for the specified Azure Managed Grafana instance.
// It retrieves the Grafana endpoint, obtains an Azure AD token, and initializes the API client.
func NewClient(ctx context.Context, credential azcore.TokenCredential, managedGrafanaClient *azure.ManagedGrafanaClient, subscriptionID, resourceGroup, grafanaName string) (*Client, error) {
	endpoint, err := managedGrafanaClient.GetGrafanaEndpoint(ctx, subscriptionID, resourceGroup, grafanaName)
	if err != nil {
		return nil, fmt.Errorf("failed to get Grafana endpoint: %w", err)
	}

	token, err := getGrafanaAPIToken(ctx, credential)
	if err != nil {
		return nil, fmt.Errorf("failed to get API token: %w", err)
	}

	return newClient(endpoint, token)
}

// newClient builds a Grafana client for the given endpoint, authenticating with
// the supplied bearer token. The token is sent as `Authorization: Bearer <token>`,
// which is what Azure Managed Grafana expects. Retries and TLS (for sovereign
// clouds) are handled natively by the transport.
func newClient(endpoint, token string) (*Client, error) {
	loc, err := parseGrafanaAPILocation(endpoint)
	if err != nil {
		return nil, err
	}

	cfg := &goapi.TransportConfig{
		Host:             loc.Host,
		BasePath:         loc.BasePath,
		Schemes:          []string{loc.Scheme},
		APIKey:           token,
		NumRetries:       grafanaAPIRetryMax,
		RetryTimeout:     grafanaAPIRetryTimeout,
		RetryStatusCodes: grafanaAPIRetryStatusCodes,
		TLSConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
	}

	apiBase, err := url.Parse(loc.Scheme + "://" + loc.Host + loc.BasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to build Grafana API base URL: %w", err)
	}

	httpClient := newRetryingHTTPClient()
	api := goapi.NewHTTPClientWithConfig(strfmt.Default, cfg)
	if err := attachHTTPDump(api, httpClient); err != nil {
		return nil, err
	}

	return &Client{
		grafanaClient: api,
		httpClient:    httpClient,
		apiBase:       apiBase,
		token:         token,
	}, nil
}

// parseGrafanaAPILocation splits a Grafana endpoint into scheme, host, and API
// base path. Schemeless endpoints are treated as https. Any path prefix on the
// endpoint (e.g. a reverse-proxy prefix) is preserved and "/api" is appended.
// An endpoint that already ends in "/api" is used as-is to avoid "/api/api".
func parseGrafanaAPILocation(endpoint string) (grafanaAPILocation, error) {
	if endpoint == "" {
		return grafanaAPILocation{}, fmt.Errorf("grafana endpoint is empty")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return grafanaAPILocation{}, fmt.Errorf("failed to parse Grafana endpoint %q: %w", endpoint, err)
	}
	if parsed.Host == "" {
		return grafanaAPILocation{}, fmt.Errorf("failed to parse Grafana endpoint %q: missing host", endpoint)
	}

	scheme := parsed.Scheme
	if scheme == "" {
		scheme = "https"
	}

	basePath := "/api"
	if prefix := strings.TrimSuffix(parsed.Path, "/"); prefix != "" {
		if strings.HasSuffix(prefix, "/api") {
			basePath = prefix
		} else {
			basePath = prefix + "/api"
		}
	}

	return grafanaAPILocation{Scheme: scheme, Host: parsed.Host, BasePath: basePath}, nil
}

func newRetryingHTTPClient() *http.Client {
	return &http.Client{
		Transport: &gtransport.RetryableTransport{
			Transport:        cloneTLSTransport(),
			NumRetries:       grafanaAPIRetryMax,
			RetryTimeout:     grafanaAPIRetryTimeout,
			RetryStatusCodes: grafanaAPIRetryStatusCodes,
		},
	}
}

func cloneTLSTransport() *http.Transport {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{TLSClientConfig: tlsCfg}
	}
	cloned := base.Clone()
	cloned.TLSClientConfig = tlsCfg
	return cloned
}

func getGrafanaAPIToken(ctx context.Context, credential azcore.TokenCredential) (string, error) {
	// ce34e7e5-485f-4d76-964f-b3d2b16d1e4f is the well-known Azure Managed Grafana service application ID
	scope := "ce34e7e5-485f-4d76-964f-b3d2b16d1e4f/.default"

	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{scope},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get token for Grafana API (scope: %s): %w", scope, err)
	}

	return token.Token, nil
}

// ListDataSources returns all datasources configured in the Grafana instance.
func (c *Client) ListDataSources(ctx context.Context) ([]Datasource, error) {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	resp, err := c.grafanaClient.Datasources.GetDataSourcesWithParams(
		datasources.NewGetDataSourcesParamsWithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get datasources: %w", err)
	}

	result := make([]Datasource, 0, len(resp.Payload))
	for _, d := range resp.Payload {
		result = append(result, Datasource{ID: uint(d.ID), UID: d.UID, Name: d.Name, Type: d.Type, URL: d.URL})
	}
	return result, nil
}

// DeleteDataSource removes a datasource from the Grafana instance by name.
func (c *Client) DeleteDataSource(ctx context.Context, dataSourceName string) error {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	_, err := c.grafanaClient.Datasources.DeleteDataSourceByNameWithParams(
		datasources.NewDeleteDataSourceByNameParamsWithContext(ctx).WithName(dataSourceName),
	)
	if err != nil {
		return fmt.Errorf("failed to delete datasource: %w", err)
	}

	return nil
}

// DeleteStaleDatasources lists all Grafana datasources and deletes any
// Managed_Prometheus_* datasource whose workspace name is not in
// validWorkspaceNames. All errors are collected and returned together.
func (c *Client) DeleteStaleDatasources(ctx context.Context, logger logr.Logger, validWorkspaceNames set.Set[string], dryRun bool) error {
	datasources, err := c.ListDataSources(ctx)
	if err != nil {
		return fmt.Errorf("failed to list Grafana datasources: %w", err)
	}

	var deleteErrors error
	for _, ds := range datasources {
		if ds.Type != "prometheus" {
			continue
		}

		workspaceName := strings.TrimPrefix(ds.Name, "Managed_Prometheus_")
		if validWorkspaceNames.Has(strings.ToLower(workspaceName)) {
			logger.Info("Keeping datasource", "datasource-name", ds.Name)
			continue
		}

		if dryRun {
			logger.Info("Dry run - would delete stale datasource", "datasource-name", ds.Name)
			continue
		}

		logger.Info("Deleting stale datasource", "datasource-name", ds.Name)
		if err := c.DeleteDataSource(ctx, ds.Name); err != nil {
			deleteErrors = errors.Join(deleteErrors, fmt.Errorf("failed to delete datasource %q: %w", ds.Name, err))
		}
	}

	if deleteErrors != nil {
		return fmt.Errorf("failed to delete stale datasources: %w", deleteErrors)
	}

	return nil
}

// WorkspaceNamesFromResourceIDs extracts workspace names (the last path
// segment) from a set of Azure resource IDs and returns them lowercased.
func WorkspaceNamesFromResourceIDs(ids set.Set[string]) set.Set[string] {
	names := set.New[string]()
	for _, id := range ids.UnsortedList() {
		name := path.Base(id)
		if name != "" && name != "." {
			names.Insert(strings.ToLower(name))
		}
	}
	return names
}

// ListFolders returns all folders in the Grafana instance.
func (c *Client) ListFolders(ctx context.Context) ([]Folder, error) {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	resp, err := c.grafanaClient.Folders.GetFolders(
		folders.NewGetFoldersParamsWithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get folders: %w", err)
	}

	result := make([]Folder, 0, len(resp.Payload))
	for _, f := range resp.Payload {
		result = append(result, Folder{UID: f.UID, Title: f.Title})
	}
	return result, nil
}

// ListDashboards returns all dashboards in the Grafana instance.
func (c *Client) ListDashboards(ctx context.Context) ([]FoundBoard, error) {
	return c.searchByType(ctx, searchTypeDashboard, "failed to search dashboards")
}

// SearchFolders returns all folders visible in the Grafana instance via the search API.
// Unlike ListFolders, search results include FolderUID which identifies parent folders.
func (c *Client) SearchFolders(ctx context.Context) ([]FoundBoard, error) {
	return c.searchByType(ctx, searchTypeFolder, "failed to search folders")
}

const (
	searchTypeDashboard = "dash-db"
	searchTypeFolder    = "dash-folder"
)

func (c *Client) searchByType(ctx context.Context, searchType, errMsg string) ([]FoundBoard, error) {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	t := searchType
	resp, err := c.grafanaClient.Search.Search(
		search.NewSearchParamsWithContext(ctx).WithType(&t),
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", errMsg, err)
	}

	result := make([]FoundBoard, 0, len(resp.Payload))
	for _, b := range resp.Payload {
		result = append(result, FoundBoard{
			UID:       b.UID,
			Title:     b.Title,
			Type:      string(b.Type),
			FolderUID: b.FolderUID,
		})
	}
	return result, nil
}

// CreateFolder creates a new folder in Grafana.
func (c *Client) CreateFolder(ctx context.Context, title string) (Folder, error) {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	resp, err := c.grafanaClient.Folders.CreateFolderWithParams(
		folders.NewCreateFolderParamsWithContext(ctx).WithBody(&models.CreateFolderCommand{Title: title}),
	)
	if err != nil {
		return Folder{}, fmt.Errorf("failed to create folder %q: %w", title, err)
	}

	f := resp.Payload
	return Folder{UID: f.UID, Title: f.Title}, nil
}

// GetDashboardByUID retrieves the metadata Grafana returns for a dashboard by
// its UID. The dashboard body itself is fetched losslessly via
// GetRawDashboardByUID; only the properties envelope is returned here.
func (c *Client) GetDashboardByUID(ctx context.Context, uid string) (BoardProperties, error) {
	_, props, err := c.GetRawDashboardByUID(ctx, uid)
	return props, err
}

// GetRawDashboardByUID retrieves a dashboard by its UID without discarding
// fields unknown to the client. The dashboard body is taken from the response
// as json.RawMessage rather than decoded through the OpenAPI `models.JSON`
// (`any`) type, which uses float64 for numbers and would not be a lossless
// round-trip.
func (c *Client) GetRawDashboardByUID(ctx context.Context, uid string) ([]byte, BoardProperties, error) {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	reqURL := c.apiBase.JoinPath("dashboards", "uid", uid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, BoardProperties{}, fmt.Errorf("failed to get dashboard %q: %w", uid, err)
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, BoardProperties{}, fmt.Errorf("failed to get dashboard %q: %w", uid, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded amount so the connection can be reused; do not keep
		// or log the body (it may contain dashboard JSON).
		_, _ = io.CopyN(io.Discard, resp.Body, 8<<10)
		return nil, BoardProperties{}, fmt.Errorf("failed to get dashboard %q: status %d", uid, resp.StatusCode)
	}

	return decodeDashboardEnvelope(resp.Body, uid, grafanaDashboardMaxBytes)
}

func decodeDashboardEnvelope(r io.Reader, uid string, maxBytes int64) ([]byte, BoardProperties, error) {
	// Read a bounded buffer first so the size check does not depend on
	// json.Decoder read-ahead against a LimitedReader.
	raw, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, BoardProperties{}, fmt.Errorf("failed to read dashboard %q: %w", uid, err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, BoardProperties{}, fmt.Errorf("dashboard %q: response exceeds %d bytes", uid, maxBytes)
	}

	var envelope struct {
		Dashboard json.RawMessage       `json:"dashboard"`
		Meta      *models.DashboardMeta `json:"meta"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&envelope); err != nil {
		return nil, BoardProperties{}, fmt.Errorf("failed to decode dashboard %q: %w", uid, err)
	}
	if len(envelope.Dashboard) == 0 || string(envelope.Dashboard) == "null" {
		return nil, BoardProperties{}, fmt.Errorf("dashboard %q: missing dashboard body", uid)
	}

	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, BoardProperties{}, fmt.Errorf("dashboard %q: response contains multiple JSON values", uid)
		}
		return nil, BoardProperties{}, fmt.Errorf("dashboard %q: trailing data after envelope: %w", uid, err)
	}

	return []byte(envelope.Dashboard), toBoardProperties(envelope.Meta), nil
}

// SetRawDashboard creates or updates a dashboard without discarding fields
// unknown to the client. The raw JSON is passed through verbatim so nothing is lost.
// The dashboard is placed in the folder identified by folderUID.
//
// Posted over raw HTTP rather than models.SaveDashboardCommand: that generated
// type marshals a zero UpdatedAt ("0001-01-01T00:00:00.000Z") the old SDK never
// sent, and Grafana ignores it. Keep the body to dashboard + folderUid + overwrite.
func (c *Client) SetRawDashboard(ctx context.Context, dashboard []byte, folderUID string, overwrite bool) error {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	payload, err := json.Marshal(struct {
		Dashboard json.RawMessage `json:"dashboard"`
		FolderUID string          `json:"folderUid,omitempty"`
		Overwrite bool            `json:"overwrite"`
	}{
		Dashboard: json.RawMessage(dashboard),
		FolderUID: folderUID,
		Overwrite: overwrite,
	})
	if err != nil {
		return fmt.Errorf("failed to set dashboard: %w", err)
	}

	reqURL := c.apiBase.JoinPath("dashboards", "db")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL.String(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to set dashboard: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to set dashboard: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("failed to set dashboard: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("failed to set dashboard: status %d", resp.StatusCode)
	}

	return nil
}

// DeleteDashboardByUID removes a dashboard by its UID.
func (c *Client) DeleteDashboardByUID(ctx context.Context, uid string) error {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	_, err := c.grafanaClient.Dashboards.DeleteDashboardByUIDWithParams(
		dashboards.NewDeleteDashboardByUIDParamsWithContext(ctx).WithUID(uid),
	)
	if err != nil {
		return fmt.Errorf("failed to delete dashboard %q: %w", uid, err)
	}

	return nil
}

// GetFolderPermissions returns the permission list for a folder.
func (c *Client) GetFolderPermissions(ctx context.Context, folderUID string) ([]FolderPermission, error) {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	resp, err := c.grafanaClient.Folders.GetFolderPermissionListWithParams(
		folders.NewGetFolderPermissionListParamsWithContext(ctx).WithFolderUID(folderUID),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get permissions for folder %q: %w", folderUID, err)
	}

	result := make([]FolderPermission, 0, len(resp.Payload))
	for _, p := range resp.Payload {
		result = append(result, FolderPermission{Role: p.Role, Permission: PermissionType(p.Permission)})
	}
	return result, nil
}

// UpdateFolderPermissions replaces the full permission list for a folder.
func (c *Client) UpdateFolderPermissions(ctx context.Context, folderUID string, permissions ...FolderPermission) error {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	items := make([]*models.DashboardACLUpdateItem, 0, len(permissions))
	for _, p := range permissions {
		items = append(items, &models.DashboardACLUpdateItem{Role: p.Role, Permission: models.PermissionType(p.Permission)})
	}

	_, err := c.grafanaClient.Folders.UpdateFolderPermissionsWithParams(
		folders.NewUpdateFolderPermissionsParamsWithContext(ctx).
			WithFolderUID(folderUID).
			WithBody(&models.UpdateDashboardACLCommand{Items: items}),
	)
	if err != nil {
		return fmt.Errorf("failed to update permissions for folder %q: %w", folderUID, err)
	}
	return nil
}

// DeleteFolderByUID removes a folder by its UID.
func (c *Client) DeleteFolderByUID(ctx context.Context, uid string) error {
	ctx, cancel := context.WithTimeout(ctx, grafanaAPITimeout)
	defer cancel()

	_, err := c.grafanaClient.Folders.DeleteFolder(
		folders.NewDeleteFolderParamsWithContext(ctx).WithFolderUID(uid),
	)
	if err != nil {
		return fmt.Errorf("failed to delete folder %q: %w", uid, err)
	}
	return nil
}

// toBoardProperties converts the dashboard metadata envelope returned by the API
// into the internal BoardProperties type.
func toBoardProperties(meta *models.DashboardMeta) BoardProperties {
	if meta == nil {
		return BoardProperties{}
	}
	return BoardProperties{Created: time.Time(meta.Created), FolderUID: meta.FolderUID}
}
