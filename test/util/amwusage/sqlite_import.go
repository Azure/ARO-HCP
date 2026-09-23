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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const scanGrouping = "cluster,job,namespace,hostedcontrolplane,prometheus"

// Initialize registers schema-1 evidence durably before importing and planning
// in one transaction. Once registration completes, Initialize(ctx, nil) or Scan
// can recover without the input. An interrupted upload still needs the input.
// A supplied seed must match the registered byte fingerprint. Temporary compressed
// seed chunks are removed atomically with plan publication; successful raw bodies
// are not retained afterwards. Context is provenance, not verified truth.
func (s *ScanStore) Initialize(ctx context.Context, seed io.Reader) error {
	if seed != nil {
		if err := s.stageBootstrap(ctx, seed); err != nil {
			return err
		}
	}
	return s.resumeBootstrap(ctx)
}

type scanSeedData struct {
	artifactData
	Context json.RawMessage `json:"context"`
}

func readScanSeed(seed io.Reader) (scanSeedData, string, error) {
	hash := sha256.New()
	limited := &io.LimitedReader{R: seed, N: (512 << 20) + 1}
	decoder := json.NewDecoder(io.TeeReader(limited, hash))
	var seedData scanSeedData
	if err := decoder.Decode(&seedData); err != nil {
		return seedData, "", err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return seedData, "", errors.New("seed must contain exactly one JSON object (maximum 512 MiB)")
	}
	if limited.N == 0 {
		return seedData, "", errors.New("seed exceeds 512 MiB")
	}
	fingerprint := hex.EncodeToString(hash.Sum(nil))
	return seedData, fingerprint, nil
}

func importScanSeed(ctx context.Context, tx *sql.Tx, seed io.Reader, expectedHash string) error {
	seedData, fingerprint, err := readScanSeed(seed)
	if err != nil {
		return err
	}
	data := seedData.artifactData
	if len(seedData.Context) > 0 {
		if err := json.Unmarshal(seedData.Context, &data.Context); err != nil {
			return err
		}
	}
	if fingerprint != expectedHash {
		return errors.New("seed fingerprint differs from registered bootstrap")
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT seed_hash FROM run WHERE id=1`).Scan(&existing)
	if err == nil {
		if existing != fingerprint {
			return errors.New("seed fingerprint differs from initialized database")
		}
		// Only incomplete plans reach here. Rebuild their derived state in this
		// transaction, so a failed replay leaves the old state and seed intact.
		for _, statement := range []string{
			`DELETE FROM catalog_observation`, `DELETE FROM workspace_identity`,
			`DELETE FROM observation`,
			`UPDATE query SET current_attempt_id=NULL,accepted_attempt_id=NULL`,
			`DELETE FROM attempt`, `DELETE FROM query`,
			`DELETE FROM platform_observation`, `DELETE FROM evidence`,
			`DELETE FROM labelset_member`, `DELETE FROM labelset`,
			`DELETE FROM label_name`, `DELETE FROM label_value`,
			`DELETE FROM metric`, `DELETE FROM workspace`, `DELETE FROM window`, `DELETE FROM run`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if data.SchemaVersion != 1 || data.Run == nil || len(data.Workspaces) == 0 || data.Run.End <= data.Run.Start || math.Trunc(data.Run.Start) != data.Run.Start || math.Trunc(data.Run.End) != data.Run.End {
		return errors.New("seed requires schema 1, whole-second run window and workspace catalogs")
	}
	contextJSON := seedData.Context
	if len(contextJSON) == 0 {
		contextJSON = json.RawMessage("null")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO run(id,seed_hash,job,build,prow,start,end,platform_start,platform_end,context_json,planning_state,created_at) VALUES(1,?,?,?,?,?,?,?,?,?,'planning',?)`, fingerprint, data.Run.Job, data.Run.Build, data.Run.Prow, data.Run.Start, data.Run.End, data.Run.PlatformStart, data.Run.PlatformEnd, string(contextJSON), scanTime(time.Now()))
	if err != nil {
		return err
	}
	windows := []struct {
		kind       string
		start, end float64
	}{{"before12h", data.Run.Start - 43200, data.Run.Start}, {"end12h", data.Run.End - 43200, data.Run.End}, {"run", data.Run.Start, data.Run.End}}
	for _, w := range windows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO window(run_id,kind,start,end) VALUES(1,?,?,?)`, w.kind, w.start, w.end); err != nil {
			return err
		}
	}
	if data.Context != nil && data.Context.Baseline.Start != "" {
		start, e1 := time.Parse(time.RFC3339Nano, data.Context.Baseline.Start)
		end, e2 := time.Parse(time.RFC3339Nano, data.Context.Baseline.End)
		if e1 != nil || e2 != nil || !end.After(start) {
			return errors.New("invalid seed baseline")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO window(run_id,kind,start,end) VALUES(1,'baseline',?,?)`, scanTime(start), scanTime(end)); err != nil {
			return err
		}
	}
	for _, w := range data.Workspaces {
		if !collectionResourceID.MatchString(w.ID) {
			return fmt.Errorf("invalid workspace ARM ID: %s", w.Name)
		}
		endpoint := strings.TrimSuffix(w.Endpoint, "/")
		blocked := ""
		// Only explicit failed requests with no claimed catalog may block a single
		// workspace. Malformed or mismatched successful evidence still rejects the seed.
		if len(w.Names) == 0 && len(w.Metrics) == 0 && len(w.Inventories) == 0 {
			if record, ok := data.Records[w.Discovery]; ok && !record.OK && endpoint != "" {
				u, err := url.Parse(record.Request.URL)
				if err == nil && u.Scheme+"://"+u.Host == endpoint && u.Path == "/api/v1/label/__name__/values" {
					start, e1 := time.Parse(time.RFC3339Nano, u.Query().Get("start"))
					end, e2 := time.Parse(time.RFC3339Nano, u.Query().Get("end"))
					if e1 == nil && e2 == nil && scanTime(start) == data.Run.Start && scanTime(end) == data.Run.End {
						blocked = "discovery_failed: metric catalog unknown; collect a fresh seed to retry"
					}
				}
			}
			if endpoint == "" {
				for _, record := range data.Records {
					u, err := url.Parse(record.Request.URL)
					if err == nil && !record.OK && u.Scheme == "https" && u.Host == "management.azure.com" && strings.EqualFold(u.Path, w.ID) {
						blocked = "endpoint_failed: workspace endpoint unknown; collect a fresh seed to retry"
						// An inert, unique identifier, never an HTTP authentication target.
						endpoint = "unavailable:" + scanHash(strings.ToLower(w.ID))
						break
					}
				}
			}
		}
		if err := validateScanEndpoint(endpoint); err != nil && !strings.HasPrefix(blocked, "endpoint_failed:") {
			return err
		}
		if blocked == "" && (w.Discovery != "" || len(w.Names) == 0) {
			if err := validateCatalog(data, w); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO workspace(run_id,arm_id,name,endpoint,blocked_reason) VALUES(1,?,?,?,?)`, strings.ToLower(w.ID), w.Name, endpoint, blocked)
		if err != nil {
			return err
		}
		wid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		account, err := seedWorkspaceAccount(data, w)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_identity(workspace_id,account_id) VALUES(?,?)`, wid, account); err != nil {
			return err
		}
		metrics := map[string]int64{}
		for _, name := range w.Names {
			if !collectionMetricName.MatchString(name) {
				return fmt.Errorf("invalid catalog metric %q", name)
			}
			key := strings.ToLower(name)
			if _, ok := metrics[key]; ok {
				continue
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO metric(workspace_id,name,display_name) VALUES(?,?,?)`, wid, key, name)
			if err != nil {
				return err
			}
			mid, err := res.LastInsertId()
			if err != nil {
				return err
			}
			metrics[key] = mid
			if err := planRankingMetric(ctx, tx, wid, mid, name, data.Run); err != nil {
				return err
			}
		}
		// Associate archived requests with metric identities and their original role.
		type archived struct {
			metric int64
			kind   string
		}
		links := map[string]archived{}
		for _, m := range w.Metrics {
			mid := metrics[strings.ToLower(m.Name)]
			if mid == 0 {
				return fmt.Errorf("selected metric %s absent from catalog", m.Name)
			}
			for kind, id := range map[string]string{"instant": m.Instant, "range": m.Range, "new_series": m.NewSeries, "samples": m.Samples, "baseline_series": m.BaselineSeries, "baseline_samples": m.BaselineSamples} {
				if id != "" {
					links[id] = archived{mid, kind}
				}
			}
		}
		for _, m := range w.Inventories {
			mid := metrics[strings.ToLower(m.Name)]
			if mid == 0 {
				return fmt.Errorf("inventory metric %s absent from catalog", m.Name)
			}
			links[m.Before] = archived{mid, "inventory_before"}
			links[m.End] = archived{mid, "inventory_end"}
		}
		for key, r := range data.Records {
			u, e := url.Parse(r.Request.URL)
			if e != nil {
				continue
			}
			belongs := strings.EqualFold(u.Host, strings.TrimPrefix(endpoint, "https://")) || strings.EqualFold(u.Path, w.ID) || strings.HasPrefix(strings.ToLower(u.Path), strings.ToLower(w.ID)+"/")
			if !belongs {
				continue
			}
			errorBody := []byte(nil)
			truncated := false
			if !r.OK {
				errorBody, truncated = compressScanError(r.Body)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO evidence(id,workspace_id,kind,url,ok,http_status,started_at,finished_at,duration_ms,response_bytes,api_cost,error_gzip,error_truncated) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, key, wid, r.Request.Kind, r.Request.URL, r.OK, r.Status, r.StartedAt, r.FinishedAt, r.ElapsedMS, r.ResponseBytes, r.APICost, errorBody, truncated)
			if err != nil {
				return err
			}
			if link, ok := links[key]; ok {
				params := u.Query().Encode()
				id := scanHash(fmt.Sprintf("%d\n%s\n%s", wid, u.Path, params))
				var at any
				if t, e := time.Parse(time.RFC3339Nano, u.Query().Get("time")); e == nil {
					at = scanTime(t)
				} else if n, e := strconv.ParseFloat(u.Query().Get("time"), 64); e == nil {
					at = n
				}
				_, err := tx.ExecContext(ctx, `INSERT INTO query(id,workspace_id,metric_id,kind,path,params,expression,evaluation_time,state,classification) VALUES(?,?,?,?,?,?,?,?,'blocked','archive_failure') ON CONFLICT(workspace_id,path,params) DO NOTHING`, id, wid, link.metric, link.kind, u.Path, params, u.Query().Get("query"), at)
				if err != nil {
					return err
				}
				if err := importScanResponse(ctx, tx, id, r, strings.HasPrefix(link.kind, "inventory")); err != nil {
					return fmt.Errorf("import %s: %w", key, err)
				}
			}
			for metric, id := range w.Platform {
				if id == key && r.OK {
					if err := importScanPlatform(ctx, tx, key, metric, r.Body); err != nil {
						return fmt.Errorf("platform %s: %w", key, err)
					}
				}
			}
		}
		if w.Discovery != "" && blocked == "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_observation(workspace_id,seed_hash,evidence_id,metric_count,observed_at) VALUES(?,?,?,?,?)`, wid, fingerprint, w.Discovery, len(metrics), scanTime(time.Now())); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE run SET planning_state='complete',state=CASE WHEN EXISTS(SELECT 1 FROM query WHERE state IN ('pending','running','retry_wait')) THEN 'active' ELSE 'complete' END WHERE id=1`)
	if err != nil {
		return err
	}
	return nil
}

func planRankingMetric(ctx context.Context, tx *sql.Tx, wid, mid int64, name string, run *artifactRun) error {
	for _, window := range []struct {
		kind       string
		start, end float64
	}{{"before12h", run.Start - 43200, run.Start}, {"end12h", run.End - 43200, run.End}, {"run", run.Start, run.End}} {
		expression := fmt.Sprintf("count by(%s)(count_over_time(%s[12h]))", scanGrouping, name)
		if window.kind == "run" {
			expression = fmt.Sprintf("sum by(%s)(count_over_time(%s[%dms]))", scanGrouping, name, int64((run.End-run.Start)*1000))
		}
		params := url.Values{"query": {expression}, "time": {time.Unix(int64(window.end), 0).UTC().Format(time.RFC3339)}, "timeout": {"90s"}}.Encode()
		id := scanHash(fmt.Sprintf("%d\n/api/v1/query\n%s", wid, params))
		if _, err := tx.ExecContext(ctx, `INSERT INTO query(id,workspace_id,metric_id,window_id,kind,ranking,path,params,expression,evaluation_time,lookback_seconds,grouping,state) VALUES(?,?,?,(SELECT id FROM window WHERE kind=?),?,1,'/api/v1/query',?,?,?,?,?,'pending')`, id, wid, mid, window.kind, window.kind, params, expression, window.end, window.end-window.start, scanGrouping); err != nil {
			return err
		}
	}
	return nil
}

func validateScanEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !collectionPrometheusHost.MatchString(u.Host) {
		return errors.New("untrusted Prometheus endpoint")
	}
	return nil
}

func importScanResponse(ctx context.Context, tx *sql.Tx, id string, r artifactRecord, inventory bool) error {
	var status int
	if r.Status != nil {
		status = *r.Status
	}
	bytes := float64(len(r.Body))
	if r.ResponseBytes != nil {
		bytes = *r.ResponseBytes
	}
	started, finished := scanTime(time.Now()), scanTime(time.Now())
	if at, e := time.Parse(time.RFC3339Nano, r.StartedAt); e == nil {
		started = scanTime(at)
	}
	if at, e := time.Parse(time.RFC3339Nano, r.FinishedAt); e == nil {
		finished = scanTime(at)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO attempt(query_id,owner,started_at,finished_at,state,http_status,duration_ms,response_bytes,api_cost) VALUES(?,'seed',?,?,'importing',?,?,?,?)`, id, started, finished, status, r.ElapsedMS, bytes, r.APICost)
	if err != nil {
		return err
	}
	aid, err := res.LastInsertId()
	if err != nil {
		return err
	}
	meta := scanResponseMeta{}
	parseErr := errors.New("archived request failed")
	if r.OK {
		var at sql.NullFloat64
		var path string
		var ranking bool
		if err := tx.QueryRowContext(ctx, `SELECT evaluation_time,path,ranking FROM query WHERE id=?`, id).Scan(&at, &path, &ranking); err != nil {
			return err
		}
		meta, parseErr = parseScanResponse(strings.NewReader(r.Body), inventory, func(batch []scanObservation) error {
			for i, observation := range batch {
				if path == "/api/v1/query" && (!at.Valid || observation.at != at.Float64) {
					return errors.New("archived evaluation timestamp mismatch")
				}
				if ranking {
					validateScanCount(&batch[i])
				}
			}
			return insertObservations(ctx, tx, aid, batch)
		})
		if parseErr == nil && path == "/api/v1/query" && meta.resultType != "vector" {
			parseErr = errors.New("archived instant query requires vector")
		}
	}
	if parseErr != nil {
		var databaseError scanDBError
		if errors.As(parseErr, &databaseError) {
			return parseErr
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM observation WHERE attempt_id=?`, aid); err != nil {
			return err
		}
		compressed, truncated := compressScanError(r.Body)
		_, err := tx.ExecContext(ctx, `UPDATE attempt SET state='failed',classification='archive_failure',error_gzip=?,error_truncated=? WHERE id=?`, compressed, truncated, aid)
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE attempt SET state='succeeded',warnings_json=?,stats_json=?,api_cost=coalesce(?,api_cost) WHERE id=?`, meta.warnings, meta.stats, meta.cost, aid)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE query SET state='succeeded',accepted_attempt_id=?,classification='' WHERE id=?`, aid, id)
	return err
}

func importScanPlatform(ctx context.Context, tx *sql.Tx, id, metric, body string) error {
	var response platformResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		return err
	}
	for _, v := range response.Value {
		if v.ErrorCode != "" && v.ErrorCode != "Success" {
			continue
		}
		for _, series := range v.Timeseries {
			labels := map[string]string{}
			for _, m := range series.MetadataValues {
				labels[m.Name.Value] = m.Value
			}
			lid, err := internLabels(ctx, tx, labels)
			if err != nil {
				return err
			}
			for _, p := range series.Data {
				at, err := time.Parse(time.RFC3339Nano, p.Timestamp)
				if err != nil {
					return err
				}
				var value *float64
				var invalid any
				if len(p.Maximum) > 0 && string(p.Maximum) != "null" {
					var n float64
					if json.Unmarshal(p.Maximum, &n) != nil {
						invalid = string(p.Maximum)
					} else {
						value = &n
					}
				} else {
					invalid = "missing"
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO platform_observation(evidence_id,metric,labelset_id,timestamp,value,invalid_value) VALUES(?,?,?,?,?,?)`, id, metric, lid, scanTime(at), value, invalid); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
