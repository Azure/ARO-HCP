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

package amwusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"
)

// validateCatalog requires direct discovery evidence, not an inferred zero from
// a missing/failed request. Historical nonempty initialization seeds may lack it.
func validateCatalog(data artifactData, w artifactWorkspace) error {
	r, ok := data.Records[w.Discovery]
	var response struct {
		Status   string          `json:"status"`
		Data     []string        `json:"data"`
		Warnings []string        `json:"warnings"`
		Error    json.RawMessage `json:"error"`
	}
	u, err := url.Parse(r.Request.URL)
	if !ok || !r.OK || (r.Status != nil && (*r.Status < 200 || *r.Status >= 300)) ||
		err != nil || u.Scheme+"://"+u.Host != strings.TrimSuffix(w.Endpoint, "/") || u.Path != "/api/v1/label/__name__/values" ||
		json.Unmarshal([]byte(r.Body), &response) != nil || response.Status != "success" || response.Data == nil || len(response.Warnings) != 0 ||
		(len(response.Error) > 0 && string(response.Error) != "null") {
		return fmt.Errorf("workspace %s requires successful metric-name discovery evidence", w.Name)
	}
	for key, expected := range map[string]float64{"start": data.Run.Start, "end": data.Run.End} {
		at, err := time.Parse(time.RFC3339Nano, u.Query().Get(key))
		if err != nil || scanTime(at) != expected {
			return fmt.Errorf("workspace %s discovery %s differs from run window", w.Name, key)
		}
	}
	names, reported := slices.Clone(w.Names), slices.Clone(response.Data)
	slices.Sort(names)
	slices.Sort(reported)
	if !slices.Equal(names, reported) {
		return fmt.Errorf("workspace %s catalog differs from discovery evidence", w.Name)
	}
	for _, name := range names {
		if !collectionMetricName.MatchString(name) {
			return fmt.Errorf("invalid catalog metric %q", name)
		}
	}
	return nil
}

func seedWorkspaceAccount(data artifactData, w artifactWorkspace) (string, error) {
	account := ""
	for _, r := range data.Records {
		u, err := url.Parse(r.Request.URL)
		if err != nil || u.Host != "management.azure.com" || !strings.EqualFold(u.Path, w.ID) || !r.OK {
			continue
		}
		var resource struct {
			Properties struct {
				AccountID string `json:"accountId"`
				Metrics   struct {
					Endpoint string `json:"prometheusQueryEndpoint"`
				} `json:"metrics"`
			} `json:"properties"`
		}
		if json.Unmarshal([]byte(r.Body), &resource) != nil || strings.TrimSuffix(resource.Properties.Metrics.Endpoint, "/") != strings.TrimSuffix(w.Endpoint, "/") {
			return "", fmt.Errorf("workspace %s resource evidence endpoint mismatch", w.Name)
		}
		id := strings.ToLower(resource.Properties.AccountID)
		if account != "" && account != id {
			return "", fmt.Errorf("workspace %s has conflicting account identities", w.Name)
		}
		account = id
	}
	return account, nil
}

// RefreshCatalog merges discovery into the original fixed-window run. It never
// makes requests or changes accepted results. Stop scanners before refreshing.
func (s *ScanStore) RefreshCatalog(ctx context.Context, seed io.Reader) error {
	return s.refreshCatalog(ctx, seed, false)
}

// RefreshCatalogRetryEmpty also reopens directly measured empty ranking queries.
// Partition and enrichment plans remain immutable; use a new DB to remeasure them.
func (s *ScanStore) RefreshCatalogRetryEmpty(ctx context.Context, seed io.Reader) error {
	return s.refreshCatalog(ctx, seed, true)
}

func (s *ScanStore) refreshCatalog(ctx context.Context, seed io.Reader, retryEmpty bool) error {
	if seed == nil {
		return errors.New("refresh requires fresh seed input")
	}
	data, fingerprint, err := readScanSeed(seed)
	if err != nil {
		return err
	}
	if data.SchemaVersion != 1 || data.Run == nil || len(data.Workspaces) == 0 {
		return errors.New("refresh requires schema 1, run and workspace catalogs")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var start, end float64
	var platformStart, platformEnd float64
	var planning, originalHash string
	var workspaces, running int
	if err := tx.QueryRowContext(ctx, `SELECT start,end,platform_start,platform_end,planning_state,seed_hash,(SELECT count(*) FROM workspace),(SELECT count(*) FROM query WHERE state='running') FROM run WHERE id=1`).Scan(&start, &end, &platformStart, &platformEnd, &planning, &originalHash, &workspaces, &running); err != nil {
		return err
	}
	if planning != "complete" || running != 0 {
		return errors.New("refresh requires complete initialization and stopped scanners with no running leases")
	}
	if start != data.Run.Start || end != data.Run.End || platformStart != float64(data.Run.PlatformStart) || platformEnd != float64(data.Run.PlatformEnd) || workspaces != len(data.Workspaces) {
		return errors.New("refresh run/platform windows and workspace set must match original seed")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO catalog_refresh(seed_hash,spec_fingerprint,created_at,workspace_count,added_metrics,retried_empty) VALUES(?,?,?,?,0,0)`, fingerprint, originalHash, scanTime(time.Now()), workspaces)
	if err != nil {
		return err
	}
	refreshID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	added := 0
	for _, w := range data.Workspaces {
		if err := validateCatalog(data.artifactData, w); err != nil {
			return err
		}
		var wid int64
		var endpoint, account string
		if err := tx.QueryRowContext(ctx, `SELECT w.id,w.endpoint,coalesce(i.account_id,'') FROM workspace w LEFT JOIN workspace_identity i ON i.workspace_id=w.id WHERE w.arm_id=?`, strings.ToLower(w.ID)).Scan(&wid, &endpoint, &account); err != nil {
			return fmt.Errorf("refresh workspace %s: %w", w.Name, err)
		}
		freshAccount, err := seedWorkspaceAccount(data.artifactData, w)
		if err != nil {
			return err
		}
		if seen[wid] || endpoint != strings.TrimSuffix(w.Endpoint, "/") || (account != "" && account != freshAccount) {
			return fmt.Errorf("refresh workspace %s duplicated or resource incarnation changed", w.Name)
		}
		seen[wid] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_identity VALUES(?,?) ON CONFLICT(workspace_id) DO UPDATE SET account_id=excluded.account_id`, wid, freshAccount); err != nil {
			return err
		}
		for _, name := range w.Names {
			result, err := tx.ExecContext(ctx, `INSERT INTO metric(workspace_id,name,display_name) VALUES(?,?,?) ON CONFLICT(workspace_id,name) DO UPDATE SET catalog=1 WHERE metric.catalog=0`, wid, strings.ToLower(name), name)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			var mid int64
			var display string
			if err := tx.QueryRowContext(ctx, `SELECT id,display_name FROM metric WHERE workspace_id=? AND name=?`, wid, strings.ToLower(name)).Scan(&mid, &display); err != nil {
				return err
			}
			if err := planRankingMetric(ctx, tx, wid, mid, display, data.Run); err != nil {
				return err
			}
			added++
		}
		// Refresh evidence is append-only and namespaced by refresh ID. Platform
		// observations stay in the fresh JSON: mixing passes would double-count.
		r := data.Records[w.Discovery]
		evidenceID := fmt.Sprintf("catalog-refresh-%d-%d", refreshID, wid)
		if _, err := tx.ExecContext(ctx, `INSERT INTO evidence(id,workspace_id,kind,url,ok,http_status,started_at,finished_at,duration_ms,response_bytes,api_cost) VALUES(?,?,'discovery',?,1,?,?,?,?,?,?)`, evidenceID, wid, r.Request.URL, r.Status, r.StartedAt, r.FinishedAt, r.ElapsedMS, r.ResponseBytes, r.APICost); err != nil {
			return err
		}
		unique := map[string]bool{}
		for _, name := range w.Names {
			unique[strings.ToLower(name)] = true
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_observation(workspace_id,seed_hash,evidence_id,metric_count,observed_at) VALUES(?,?,?,?,?)`, wid, fingerprint, evidenceID, len(unique), scanTime(time.Now())); err != nil {
			return err
		}
	}
	var retried int64
	if retryEmpty {
		if err := ensurePartitionSchema(ctx, tx); err != nil {
			return err
		}
		// Do not reopen a derived root or a reused child of any immutable plan.
		result, err := tx.ExecContext(ctx, `UPDATE query SET state='pending',accepted_attempt_id=NULL,current_attempt_id=NULL,owner=NULL,lease_until=NULL,retry_at=0,classification=''
 WHERE ranking=1 AND state='succeeded' AND accepted_attempt_id IN (SELECT id FROM attempt WHERE state='succeeded' AND warnings_json IN ('[]','null'))
 AND NOT EXISTS(SELECT 1 FROM observation WHERE attempt_id=query.accepted_attempt_id)
 AND NOT EXISTS(SELECT 1 FROM query_partition WHERE parent_id=query.id OR child_id=query.id)
 AND NOT EXISTS(SELECT 1 FROM namespace_inventory_plan WHERE root_id=query.id)`)
		if err != nil {
			return err
		}
		retried, err = result.RowsAffected()
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_refresh SET added_metrics=?,retried_empty=? WHERE id=?`, added, retried, refreshID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE run SET state='active' WHERE EXISTS(SELECT 1 FROM query WHERE state IN ('pending','retry_wait'))`); err != nil {
		return err
	}
	return tx.Commit()
}
