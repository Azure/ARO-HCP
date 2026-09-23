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
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const scanLease = 60 * time.Second

// Audit claims and abandoned work are not retry failures. This predicate also
// recognizes completed retryable outcomes written by the initial scanner.
const scanRetryFailures = `finished_at IS NOT NULL AND owner<>'seed' AND state IN ('retry_wait','blocked') AND classification IN ('throttled','unavailable','transport','timeout')`

var errScanLease = errors.New("scan attempt lease lost")

// ScanOptions affects execution only; windows, catalogs and exact requests come
// from the database. Workers defaults to four, with one initial request per
// workspace, rising to two after ten consecutive successes.
type ScanOptions struct {
	Workers    int
	Credential azcore.TokenCredential
	Transport  http.RoundTripper
	// Kinds selects logical enrichment roles through enrichment_query links.
	// Only their exact requests are executed/recovered, without partition planning
	// or run completion updates. Empty preserves normal catalog scanning.
	Kinds []string
	// NamespaceOnly executes only immutable namespace inventory children, without
	// legacy partition planning. The durable total attempt budget includes retries.
	NamespaceOnly bool
}

type scanClaim struct {
	id, owner, endpoint, path, params, kind string
	workspace, attempt                      int64
	count                                   int
}
type scanOutcome struct {
	scanResponseMeta
	status                                         int
	classification, body, requestID, correlationID string
	retryAt                                        float64
	retry, auth                                    bool
	duration                                       int64
}

// AzureCLICredential does not cache tokens. Share one in-memory token among all
// workers rather than spawning az for each of thousands of catalog requests.
type scanCredential struct {
	mu         sync.Mutex
	credential azcore.TokenCredential
	token      azcore.AccessToken
}

func (c *scanCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return azcore.AccessToken{}, err
	}
	if c.token.ExpiresOn.After(time.Now().Add(2 * time.Minute)) {
		return c.token, nil
	}
	token, err := c.credential.GetToken(ctx, options)
	if err == nil {
		c.token = token
	}
	return token, err
}

// Scan resumes persisted work until every query has succeeded or is blocked.
// Blocked coverage is not a scheduling failure: inspect Summary afterwards.
// Cancellation releases this process's leases; crashes are recovered at expiry.
func (s *ScanStore) Scan(ctx context.Context, o ScanOptions) error {
	if o.Workers == 0 {
		o.Workers = 4
	}
	if o.Workers < 1 || o.Workers > 4 {
		return errors.New("workers must be between 1 and 4")
	}
	if o.Credential == nil {
		return errors.New("credential is required")
	}
	if o.NamespaceOnly && len(o.Kinds) > 0 {
		return errors.New("namespace and enrichment scan scopes are mutually exclusive")
	}
	for _, kind := range o.Kinds {
		if kind != "samples_window" && kind != "inventory_samples" {
			return errors.New("filtered scans only support samples_window and inventory_samples")
		}
	}
	var planning string
	err := s.db.QueryRowContext(ctx, `SELECT planning_state FROM run WHERE id=1`).Scan(&planning)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if planning != "complete" {
		if len(o.Kinds) > 0 || o.NamespaceOnly {
			return errors.New("enrichment requires a completely initialized seed")
		}
		if err := s.Initialize(ctx, nil); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE run SET max_workers=? WHERE id=1`, o.Workers); err != nil {
		return err
	}
	owner := uuid.NewString()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	transport := o.Transport
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.MaxConnsPerHost = 4
		t.MaxIdleConnsPerHost = 4
		transport = t
		defer t.CloseIdleConnections()
	}
	client := &http.Client{Transport: transport, Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	credential := &scanCredential{credential: o.Credential}
	var wg sync.WaitGroup
	errCh := make(chan error, o.Workers)
	for range o.Workers {
		wg.Add(1)
		go func() {
			defer utilruntime.HandleCrash()
			defer wg.Done()
			err := s.scanWorker(ctx, owner, credential, client, o.NamespaceOnly, o.Kinds...)
			if err != nil {
				errCh <- err
				cancel()
			}
		}()
	}
	wg.Wait()
	close(errCh)
	var failures []error
	for err := range errCh {
		failures = append(failures, err)
	}
	// Use a bounded independent context only for releasing claims after cancellation.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cleanupCancel()
	if err := s.releaseScanClaims(cleanupCtx, owner); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (s *ScanStore) scanWorker(ctx context.Context, owner string, credential azcore.TokenCredential, client *http.Client, namespaceOnly bool, kinds ...string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(kinds) == 0 && !namespaceOnly {
			if _, err := s.planHardLimitPartitions(ctx); err != nil {
				return err
			}
		}
		var claim *scanClaim
		var done bool
		var err error
		if len(kinds) > 0 {
			claim, done, err = s.claimEnrichment(ctx, owner, time.Now(), kinds)
		} else {
			claim, done, err = s.claimScan(ctx, owner, time.Now(), namespaceOnly)
		}
		if err != nil {
			return err
		}
		if done {
			if len(kinds) > 0 || namespaceOnly {
				return nil
			}
			// A concurrent publisher may have blocked a hard limit between the
			// planning and claim transactions. Plan once more before exiting.
			planned, err := s.planHardLimitPartitions(ctx)
			if err != nil {
				return err
			}
			if planned {
				continue
			}
			return nil
		}
		if claim == nil {
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if err := s.executeScan(ctx, *claim, credential, client); err != nil {
			return err
		}
	}
}

func (s *ScanStore) claimScan(ctx context.Context, owner string, now time.Time, namespaceOnly ...bool) (*scanClaim, bool, error) {
	waitStarted := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	now = now.Add(time.Since(waitStarted))
	if err := finalizeReadyPartitions(ctx, tx, now); err != nil {
		return nil, false, err
	}
	n := scanTime(now)
	filter := ""
	if len(namespaceOnly) > 0 && namespaceOnly[0] {
		filter = ` AND id IN (SELECT child_id FROM namespace_inventory_child)`
	}
	// Expired work is fenced before staging cleanup, including after process death.
	for _, statement := range []string{
		`UPDATE attempt SET state='abandoned',finished_at=?,classification='lease_expired' WHERE id IN (SELECT current_attempt_id FROM query WHERE state='running' AND lease_until<=?` + filter + `)`,
		`DELETE FROM observation WHERE attempt_id IN (SELECT current_attempt_id FROM query WHERE state='running' AND lease_until<=?` + filter + `)`,
		`UPDATE query SET state='pending',owner=NULL,lease_until=NULL,current_attempt_id=NULL WHERE state='running' AND lease_until<=?` + filter,
	} {
		args := []any{n}
		if strings.HasPrefix(statement, "UPDATE attempt") {
			args = append(args, n)
		}
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return nil, false, err
		}
	}
	if filter != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE query SET state='blocked',classification='namespace_inventory_budget' WHERE state IN ('pending','retry_wait')`+filter+`
 AND (SELECT count(*) FROM attempt WHERE id>(SELECT min(attempt_baseline) FROM namespace_inventory_plan) AND query_id IN (SELECT child_id FROM namespace_inventory_child))>=?`, namespaceInventoryBudget); err != nil {
			return nil, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE query SET state='blocked',classification='retry_budget' WHERE state IN ('pending','retry_wait')`+filter+` AND id IN (SELECT query_id FROM attempt WHERE `+scanRetryFailures+` GROUP BY query_id HAVING count(*)>=6 OR sum(duration_ms)>=900000)`); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE query SET state='blocked',classification='workspace_authorization' WHERE state IN ('pending','retry_wait')`+filter+` AND workspace_id IN (SELECT id FROM workspace WHERE blocked_reason<>'')`); err != nil {
		return nil, false, err
	}
	if err := blockInactivePartitionWork(ctx, tx); err != nil {
		return nil, false, err
	}
	if err := finalizeReadyPartitions(ctx, tx, now); err != nil {
		return nil, false, err
	}
	c := &scanClaim{owner: owner}
	err = tx.QueryRowContext(ctx, `WITH selected AS (SELECT rowid,* FROM query WHERE 1=1`+filter+`) SELECT q.id,q.workspace_id,w.endpoint,q.path,q.params,q.kind,q.attempt_count
 FROM selected q JOIN workspace w ON w.id=q.workspace_id WHERE q.state IN ('pending','retry_wait') AND q.retry_at<=? AND w.cooldown_until<=? AND w.blocked_reason=''
 AND (SELECT count(*) FROM query WHERE state='running') < (SELECT max_workers FROM run WHERE id=1)
 AND (SELECT count(*) FROM query active WHERE active.workspace_id=q.workspace_id AND active.state='running') < w.concurrency
 ORDER BY (q.attempt_count>0) DESC,(instr(q.expression,'namespace="')>0) DESC,length(q.expression),q.rowid LIMIT 1`, n, n).Scan(&c.id, &c.workspace, &c.endpoint, &c.path, &c.params, &c.kind, &c.count)
	if errors.Is(err, sql.ErrNoRows) {
		var remaining int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM query WHERE state IN ('pending','retry_wait','running')`+filter).Scan(&remaining); err != nil {
			return nil, false, err
		}
		if remaining == 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE run SET state='complete' WHERE id=1 AND NOT EXISTS(SELECT 1 FROM query WHERE state IN ('pending','retry_wait','running'))`); err != nil {
				return nil, false, err
			}
		}
		return nil, remaining == 0, tx.Commit()
	}
	if err != nil {
		return nil, false, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO attempt(query_id,owner,started_at,state) VALUES(?,?,?,'running')`, c.id, owner, n)
	if err != nil {
		return nil, false, err
	}
	c.attempt, err = res.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	c.count++
	_, err = tx.ExecContext(ctx, `UPDATE query SET state='running',owner=?,lease_until=?,current_attempt_id=?,attempt_count=attempt_count+1,first_attempt_at=coalesce(first_attempt_at,?) WHERE id=?`, owner, n+scanLease.Seconds(), c.attempt, n, c.id)
	if err != nil {
		return nil, false, err
	}
	return c, false, tx.Commit()
}

func (s *ScanStore) executeScan(ctx context.Context, c scanClaim, credential azcore.TokenCredential, client *http.Client) error {
	leaseCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	requestCtx, cancel := context.WithTimeout(leaseCtx, 120*time.Second)
	defer cancel()
	heartbeatDone := make(chan error, 1)
	go func() {
		defer utilruntime.HandleCrash()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				res, err := s.db.ExecContext(leaseCtx, `UPDATE query SET lease_until=unixepoch('subsec')+60 WHERE id=? AND owner=? AND current_attempt_id=? AND state='running' AND lease_until>unixepoch('subsec')`, c.id, c.owner, c.attempt)
				if err == nil {
					var n int64
					n, err = res.RowsAffected()
					if n != 1 {
						// Publication may commit immediately before this renewal.
						var finished bool
						err = s.db.QueryRowContext(leaseCtx, `SELECT finished_at IS NOT NULL FROM attempt WHERE id=? AND owner=?`, c.attempt, c.owner).Scan(&finished)
						if err == nil && finished {
							heartbeatDone <- nil
							return
						}
						if err == nil {
							err = errScanLease
						}
					}
				}
				if err != nil && leaseCtx.Err() == nil {
					heartbeatDone <- err
					stopHeartbeat()
					return
				}
			}
		}
	}()
	outcome, err := s.requestScan(requestCtx, c, credential, client)
	cancel()
	// Renewal stays active while publication waits for the writer connection.
	if err == nil && ctx.Err() == nil {
		err = s.finishScan(leaseCtx, c, outcome, time.Now())
	}
	stopHeartbeat()
	heartbeatErr := <-heartbeatDone
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if heartbeatErr != nil {
		return heartbeatErr
	}
	if err != nil {
		return err
	}
	return nil
}

func (s *ScanStore) requestScan(ctx context.Context, c scanClaim, credential azcore.TokenCredential, client *http.Client) (o scanOutcome, resultErr error) {
	o = scanOutcome{scanResponseMeta: scanResponseMeta{warnings: "[]", stats: "{}"}}
	if err := validateScanEndpoint(c.endpoint); err != nil {
		return o, err
	}
	if c.path != "/api/v1/query" && c.path != "/api/v1/query_range" {
		return o, errors.New("untrusted query path")
	}
	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://prometheus.monitor.azure.com/.default"}})
	if err != nil {
		if ctx.Err() != nil {
			o.classification = "timeout"
			o.retry = true
			o.body = "Azure token acquisition timed out"
			return o, nil
		}
		o.classification = "authentication"
		o.auth = true
		o.body = "Azure token acquisition failed; verify Azure CLI login and workspace query permissions"
		return o, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+c.path+"?"+c.params, nil)
	if err != nil {
		return o, err
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Accept", "application/json")
	started := time.Now()
	defer func() { o.duration = time.Since(started).Milliseconds() }()
	response, err := client.Do(req)
	if err != nil {
		o.classification = "transport"
		o.retry = true
		o.body = "HTTP transport failure or timeout"
		return o, nil
	}
	defer response.Body.Close()
	o.status = response.StatusCode
	o.requestID = response.Header.Get("x-ms-request-id")
	if o.requestID == "" {
		o.requestID = response.Header.Get("x-request-id")
	}
	o.correlationID = response.Header.Get("x-ms-correlation-request-id")
	o.requestID = o.requestID[:min(len(o.requestID), 1024)]
	o.correlationID = o.correlationID[:min(len(o.correlationID), 1024)]
	if o.status < 200 || o.status >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 65537))
		o.body = strings.ReplaceAll(string(body), token.Token, "[REDACTED]")
		o.bytes = int64(len(body))
		o.classification, o.retry, o.auth = classifyScanFailure(o.status, o.body)
		if readErr != nil && o.classification != "hard_limit" && !o.auth {
			o.classification = "transport"
			o.retry = true
		}
		o.retryAt = scanTime(scanRetryAfter(response.Header.Get("Retry-After"), time.Now()))
		return o, nil
	}
	// A bounded prefix is retained only on failure; successful bodies are streamed
	// straight into staging batches, never held as a raw JSON response.
	prefix := &scanPrefix{}
	params, err := url.ParseQuery(c.params)
	if err != nil {
		return o, err
	}
	var expectedTime float64
	if c.path == "/api/v1/query" {
		if at, e := time.Parse(time.RFC3339Nano, params.Get("time")); e == nil {
			expectedTime = scanTime(at)
		} else {
			expectedTime, err = strconv.ParseFloat(params.Get("time"), 64)
			if err != nil {
				return o, err
			}
		}
	}
	meta, parseErr := parseScanResponse(io.TeeReader(response.Body, prefix), strings.HasPrefix(c.kind, "inventory"), func(batch []scanObservation) error {
		for i := range batch {
			if c.path == "/api/v1/query" && batch[i].at != expectedTime {
				return errors.New("sample timestamp differs from requested evaluation")
			}
			if c.kind == "before12h" || c.kind == "end12h" || c.kind == "run" || c.kind == "samples_window" || c.kind == "inventory_samples" {
				validateScanCount(&batch[i])
			}
			if (c.kind == "samples_window" || c.kind == "inventory_samples") && (batch[i].integer == nil || *batch[i].integer <= 0) {
				raw := strings.ReplaceAll(batch[i].raw, token.Token, "[REDACTED]")
				return fmt.Errorf("sample counts must be positive integers: value=%q at=%g", raw[:min(len(raw), 512)], batch[i].at)
			}
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return scanDBError{err}
		}
		defer func() { _ = tx.Rollback() }()
		if err := checkScanFence(ctx, tx, c, time.Now()); err != nil {
			return err
		}
		if err := insertObservations(ctx, tx, c.attempt, batch); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return scanDBError{err}
		}
		return nil
	})
	o.scanResponseMeta = meta
	if parseErr == nil && c.path == "/api/v1/query" && meta.resultType != "vector" {
		parseErr = errors.New("instant query did not return a vector")
	}
	if parseErr != nil {
		var databaseError scanDBError
		if (errors.As(parseErr, &databaseError) && ctx.Err() == nil) || errors.Is(parseErr, errScanLease) {
			return o, parseErr
		}
		o.classification = "invalid_response"
		o.body = strings.ReplaceAll(string(prefix.data), token.Token, "[REDACTED]")
		if class, _, _ := classifyScanFailure(200, o.body); class == "hard_limit" {
			o.classification = class
		}
		// Keep late parse failures ahead of the raw prefix so compression's
		// 64 KiB cap cannot discard the diagnostic. Redact before truncating.
		diagnostic := strings.ReplaceAll(parseErr.Error(), token.Token, "[REDACTED]")
		o.body = "Response validation: " + diagnostic[:min(len(diagnostic), 1024)] + "\nResponse prefix:\n" + o.body
		var networkError net.Error
		var bodyError scanBodyReadError
		if errors.As(parseErr, &bodyError) && o.classification != "hard_limit" {
			o.retry = true
			o.classification = "transport"
		}
		if o.classification != "hard_limit" && (ctx.Err() != nil || (errors.As(parseErr, &networkError) && networkError.Timeout())) {
			o.retry = true
			o.classification = "timeout"
		}
		return o, nil
	}
	return o, nil
}

type scanPrefix struct{ data []byte }

func (p *scanPrefix) Write(b []byte) (int, error) {
	n := len(b)
	if len(p.data) < 65537 {
		keep := min(n, 65537-len(p.data))
		p.data = append(p.data, b[:keep]...)
	}
	return n, nil
}

func classifyScanFailure(status int, body string) (classification string, retry, auth bool) {
	if status == 401 || status == 403 {
		return "authorization", false, true
	}
	b := strings.ToLower(body)
	if strings.Contains(b, "timeseries per metric limit") || strings.Contains(b, "time series per metric limit") || strings.Contains(b, "estimated query cost") || (strings.Contains(b, "query") && strings.Contains(b, "exceed") && (strings.Contains(b, "15000") || strings.Contains(b, "series limit"))) {
		return "hard_limit", false, false
	}
	if status == 429 {
		return "throttled", true, false
	}
	if status == 502 || status == 503 || status == 504 {
		return "unavailable", true, false
	}
	return "http_error", false, false
}

func scanRetryAfter(value string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 && seconds <= 31536000 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date
	}
	return now
}

func checkScanFence(ctx context.Context, tx *sql.Tx, c scanClaim, now time.Time) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM query WHERE id=? AND owner=? AND current_attempt_id=? AND state='running' AND lease_until>?`, c.id, c.owner, c.attempt, scanTime(now)).Scan(&n); err != nil {
		return scanDBError{err}
	}
	if n != 1 {
		return errScanLease
	}
	return nil
}

func (s *ScanStore) finishScan(ctx context.Context, c scanClaim, o scanOutcome, now time.Time) error {
	waitStarted := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now = now.Add(time.Since(waitStarted))
	if err := checkScanFence(ctx, tx, c, now); err != nil {
		return err
	}
	n := scanTime(now)
	state := "succeeded"
	var accepted any = c.attempt
	var compressed []byte
	truncated := false
	budgetExhausted := false
	if o.classification != "" {
		state = "blocked"
		accepted = nil
		if _, err := tx.ExecContext(ctx, `UPDATE workspace SET success_streak=0 WHERE id=?`, c.workspace); err != nil {
			return err
		}
		compressed, truncated = compressScanError(o.body)
		if o.retry {
			var failures int
			var duration int64
			if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(duration_ms),0) FROM attempt WHERE query_id=? AND `+scanRetryFailures, c.id).Scan(&failures, &duration); err != nil {
				return err
			}
			failures++
			backoff := time.Duration(1<<min(failures, 6)) * time.Second
			delay := time.Second + time.Duration(rand.Int64N(int64(min(backoff, 60*time.Second)-time.Second+1)))
			o.retryAt = max(o.retryAt, scanTime(now.Add(delay)))
			budgetExhausted = failures >= 6 || duration+o.duration >= 900000
			if !budgetExhausted {
				state = "retry_wait"
			}
			if _, err := tx.ExecContext(ctx, `UPDATE workspace SET cooldown_until=max(cooldown_until,?),success_streak=0,concurrency=1 WHERE id=?`, o.retryAt, c.workspace); err != nil {
				return err
			}
		}
		var blocked string
		if err := tx.QueryRowContext(ctx, `SELECT blocked_reason FROM workspace WHERE id=?`, c.workspace).Scan(&blocked); err != nil {
			return err
		}
		if blocked != "" {
			state = "blocked"
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM observation WHERE attempt_id=?`, c.attempt); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE workspace SET success_streak=success_streak+1,concurrency=CASE WHEN success_streak+1>=10 THEN 2 ELSE concurrency END WHERE id=?`, c.workspace); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE attempt SET state=?,finished_at=?,http_status=?,response_bytes=?,duration_ms=?,api_cost=?,request_id=?,correlation_id=?,classification=?,error_gzip=?,error_truncated=?,warnings_json=?,stats_json=? WHERE id=?`, state, n, o.status, o.bytes, o.duration, o.cost, o.requestID, o.correlationID, o.classification, compressed, truncated, o.warnings, o.stats, c.attempt)
	if err != nil {
		return err
	}
	classification := o.classification
	if budgetExhausted {
		classification = "retry_budget"
	}
	_, err = tx.ExecContext(ctx, `UPDATE query SET state=?,accepted_attempt_id=?,owner=NULL,lease_until=NULL,current_attempt_id=NULL,retry_at=?,classification=? WHERE id=?`, state, accepted, o.retryAt, classification, c.id)
	if err != nil {
		return err
	}
	if o.auth {
		if _, err := tx.ExecContext(ctx, `UPDATE workspace SET blocked_reason='Authentication/authorization failed; verify login and Monitoring Data Reader access before unblocking' WHERE id=?`, c.workspace); err != nil {
			return err
		}
		// Other in-flight requests may finish, but no more work is admitted here.
		filter := ""
		if c.kind == "samples_window" || c.kind == "inventory_samples" {
			filter = " AND id IN (SELECT query_id FROM enrichment_query WHERE plan_id=1)"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE query SET state='blocked',classification='workspace_authorization' WHERE workspace_id=? AND state IN ('pending','retry_wait')`+filter, c.workspace); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ScanStore) releaseScanClaims(ctx context.Context, owner string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM observation WHERE attempt_id IN (SELECT current_attempt_id FROM query WHERE owner=? AND state='running')`, owner); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE attempt SET state='abandoned',classification='canceled',finished_at=? WHERE id IN (SELECT current_attempt_id FROM query WHERE owner=? AND state='running')`, scanTime(time.Now()), owner); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE query SET state='pending',owner=NULL,lease_until=NULL,current_attempt_id=NULL WHERE owner=? AND state='running'`, owner); err != nil {
		return err
	}
	return tx.Commit()
}
