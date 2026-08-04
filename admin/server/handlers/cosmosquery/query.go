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

package cosmosquery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	defaultMaxItems = 100
	maxMaxItems     = 1000
	maxRequestBody  = 1 * 1024 * 1024 // 1MB
)

var mutatingKeywordPattern *regexp.Regexp

func init() {
	keywords := []string{
		"UPDATE", "DELETE", "INSERT", "CREATE", "DROP",
		"TRUNCATE", "ALTER", "MERGE", "UPSERT", "REPLACE",
		"EXEC", "EXECUTE", "SET",
	}
	pattern := `(?i)\b(` + strings.Join(keywords, "|") + `)\b`
	mutatingKeywordPattern = regexp.MustCompile(pattern)
}

type QueryRequest struct {
	ContainerName string `json:"containerName"`
	Query         string `json:"query"`
	PartitionKey  string `json:"partitionKey,omitempty"`
	MaxItems      int32  `json:"maxItems,omitempty"`
}

type QueryResponse struct {
	Results []json.RawMessage `json:"results"`
	Count   int               `json:"count"`
}

type QueryHandler struct {
	cosmosDatabaseClient *azcosmos.DatabaseClient
}

func NewQueryHandler(cosmosDatabaseClient *azcosmos.DatabaseClient) *QueryHandler {
	return &QueryHandler{cosmosDatabaseClient: cosmosDatabaseClient}
}

func (h *QueryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	if h.cosmosDatabaseClient == nil {
		return arm.NewCloudError(
			http.StatusInternalServerError,
			arm.CloudErrorCodeInternalServerError,
			"",
			"CosmosDB query endpoint is not available",
		)
	}

	req, err := parseQueryRequest(r)
	if err != nil {
		return err
	}

	if err := validateReadOnlyQuery(req.Query); err != nil {
		return arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent,
			"",
			"%s", err,
		)
	}

	return executeQuery(r.Context(), w, h.cosmosDatabaseClient, req)
}

func parseQueryRequest(r *http.Request) (*QueryRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		return nil, utils.TrackError(err)
	}

	var req QueryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent,
			"",
			"Failed to parse request body: %s", err,
		)
	}

	if req.ContainerName == "" {
		return nil, arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent,
			"",
			"containerName is required",
		)
	}

	if req.Query == "" {
		return nil, arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent,
			"",
			"query is required",
		)
	}

	return &req, nil
}

func executeQuery(ctx context.Context, w http.ResponseWriter, db *azcosmos.DatabaseClient, req *QueryRequest) error {
	maxItems := int32(defaultMaxItems)
	if req.MaxItems > 0 {
		maxItems = req.MaxItems
	}
	if maxItems > maxMaxItems {
		return arm.NewCloudError(
			http.StatusBadRequest,
			arm.CloudErrorCodeInvalidRequestContent,
			"",
			"maxItems must not exceed %d", maxMaxItems,
		)
	}

	containerClient, err := db.NewContainer(req.ContainerName)
	if err != nil {
		return utils.TrackError(err)
	}

	queryOpts := azcosmos.QueryOptions{
		PageSizeHint: maxItems,
	}

	var pk azcosmos.PartitionKey
	if req.PartitionKey != "" {
		pk = azcosmos.NewPartitionKeyString(req.PartitionKey)
	}

	pager := containerClient.NewQueryItemsPager(req.Query, pk, &queryOpts)

	var results []json.RawMessage
	collected := int32(0)

	for pager.More() && collected < maxItems {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return utils.TrackError(err)
		}
		for _, item := range page.Items {
			if collected >= maxItems {
				break
			}
			results = append(results, json.RawMessage(item))
			collected++
		}
	}

	if results == nil {
		results = []json.RawMessage{}
	}

	resp := QueryResponse{
		Results: results,
		Count:   len(results),
	}

	_, err = arm.WriteJSONResponse(w, http.StatusOK, resp)
	return utils.TrackError(err)
}

// stripStringLiterals removes single-quoted string literals from a query
// to prevent false positives during keyword detection.
func stripStringLiterals(query string) string {
	var result strings.Builder
	inString := false
	i := 0
	for i < len(query) {
		if query[i] == '\'' {
			if inString {
				// Check for escaped quote ('')
				if i+1 < len(query) && query[i+1] == '\'' {
					i += 2
					continue
				}
				inString = false
				i++
				continue
			}
			inString = true
			i++
			continue
		}
		if !inString {
			result.WriteByte(query[i])
		}
		i++
	}
	return result.String()
}

func validateReadOnlyQuery(query string) error {
	stripped := stripStringLiterals(query)
	match := mutatingKeywordPattern.FindString(stripped)
	if match != "" {
		return fmt.Errorf("query contains mutating keyword %q; only read-only queries are allowed", strings.ToUpper(match))
	}
	return nil
}
