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
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxPartitionChildren = 1024

const maxNamespaceDepth = 4

var errPartitionExhausted = errors.New("partition plan exhausted")

const partitionSchema = `CREATE TABLE IF NOT EXISTS query_partition (
 parent_id TEXT NOT NULL REFERENCES query(id), child_id TEXT NOT NULL UNIQUE REFERENCES query(id),
 key TEXT NOT NULL CHECK(key IN ('cluster','namespace')),
 parent_predicate TEXT NOT NULL, child_predicate TEXT NOT NULL,
 active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
 PRIMARY KEY(parent_id,child_id), CHECK(parent_id<>child_id)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS namespace_partition (
 query_id TEXT PRIMARY KEY REFERENCES query(id), suffix TEXT NOT NULL,
 bucket TEXT NOT NULL, depth INTEGER NOT NULL CHECK(depth BETWEEN 0 AND 4),
 terminal INTEGER NOT NULL CHECK(terminal IN (0,1))
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS fallback_partition (
 query_id TEXT PRIMARY KEY REFERENCES query(id), phase TEXT NOT NULL CHECK(phase IN ('instance','job')),
 suffix TEXT NOT NULL, bucket TEXT NOT NULL, depth INTEGER NOT NULL CHECK(depth BETWEEN 0 AND 4),
 terminal INTEGER NOT NULL CHECK(terminal IN (0,1))
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS job_partition (
 query_id TEXT PRIMARY KEY REFERENCES query(id), excluded_json TEXT NOT NULL,
 residual INTEGER NOT NULL CHECK(residual IN (0,1))
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS partition_version (version INTEGER PRIMARY KEY)`

func ensurePartitionSchema(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, partitionSchema); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, namespaceInventorySchema); err != nil {
		return err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('query_partition') WHERE name='active'`).Scan(&active); err != nil {
		return err
	}
	if active == 0 {
		_, err := tx.ExecContext(ctx, `ALTER TABLE query_partition ADD COLUMN active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1))`)
		return err
	}
	return nil
}

// Singleton suffix classes can use AMW's case-insensitive literal equality.
// Negated classes are not singleton boundaries and must retain their regex.
func namespaceLiteral(p namespacePartition) (string, bool) {
	if !p.terminal {
		return "", false
	}
	var literal strings.Builder
	for s := p.suffix; s != ""; s = s[3:] {
		if len(s) < 3 || s[0] != '[' || s[2] != ']' || !strings.ContainsRune("0123456789abcdefghijklmnopqrstuvwxyz", rune(s[1])) {
			return "", false
		}
		literal.WriteByte(s[1])
	}
	return literal.String(), true
}

type namespacePartition struct {
	suffix, bucket string
	depth          int
	terminal       bool
}

// Each level partitions the final rune: four ASCII bins and their positive
// character-class complement. The short-string leaf covers the suffix itself,
// including empty/absent namespace at the first level. Explicit case folding
// matches AMW semantics; dotall also covers arbitrary labels containing newlines.
func namespaceParts(p *namespacePartition) ([]namespacePartition, error) {
	suffix, depth := "", 1
	if p != nil {
		if p.terminal {
			return nil, fmt.Errorf("%w: exact namespace suffix cannot be refined", errPartitionExhausted)
		}
		if !strings.HasPrefix(p.bucket, "^") && len(p.bucket) > 1 {
			var parts []namespacePartition
			for _, char := range p.bucket {
				parts = append(parts, namespacePartition{suffix: p.suffix, bucket: string(char), depth: p.depth})
			}
			return parts, nil
		}
		if p.depth >= maxNamespaceDepth {
			return nil, fmt.Errorf("%w: namespace suffix depth", errPartitionExhausted)
		}
		suffix, depth = "["+p.bucket+"]"+p.suffix, p.depth+1
	}
	var parts []namespacePartition
	for _, bucket := range []string{"0123456789", "abcdef", "ghijklmnop", "qrstuvwxyz", "^0-9a-z"} {
		parts = append(parts, namespacePartition{suffix: suffix, bucket: bucket, depth: depth})
	}
	return append(parts, namespacePartition{suffix: suffix, depth: depth - 1, terminal: true}), nil
}

func (p namespacePartition) matcher() string {
	if literal, ok := namespaceLiteral(p); ok {
		return "namespace=" + strconv.Quote(literal)
	}
	pattern := p.suffix
	if !p.terminal {
		pattern = ".*[" + p.bucket + "]" + pattern
	}
	return "namespace=~" + strconv.Quote("^(?is:"+pattern+")$")
}

// Port-shaped instances must have a nonempty host. The complement includes
// absent/empty labels, bare hosts, malformed ports AND empty-host ':443'.
func instanceParts() []namespacePartition {
	var parts []namespacePartition
	for _, digit := range "0123456789" {
		parts = append(parts, namespacePartition{bucket: string(digit), depth: 1})
	}
	return append(parts, namespacePartition{bucket: "^0-9", depth: 1}, namespacePartition{bucket: "nonport", terminal: true})
}

func instanceMatcher(p namespacePartition) string {
	if p.bucket == "nonport" {
		return `instance!~"^(?s:.+:[0-9]+)$"`
	}
	pattern := p.suffix + ":[0-9]+"
	if !p.terminal {
		pattern = ".*[" + p.bucket + "]" + pattern
	}
	return "instance=~" + strconv.Quote("^(?is:"+pattern+")$")
}

// partitionPredicates adds a conjunction to the parent's predicate. Exact values
// plus their complement cover even unseen and absent labels; observed inventory
// is an optimization, never an assumption that the inventory is exhaustive.
func partitionPredicates(parent, key string, values []string) ([]string, error) {
	if key != "cluster" && key != "namespace" && key != "job" {
		return nil, errors.New("unsupported partition label")
	}
	unique := map[string]bool{}
	for _, value := range values {
		unique[value] = true
	}
	if len(unique) == 0 || len(unique)+1 > maxPartitionChildren {
		return nil, fmt.Errorf("%w: inventory is empty or exceeds child quota", errPartitionExhausted)
	}
	values = make([]string, 0, len(unique))
	for value := range unique {
		values = append(values, value)
	}
	sort.Strings(values)
	prefix := parent
	if prefix != "" {
		prefix += ","
	}
	parts := make([]string, 0, len(values)+1)
	escaped := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, prefix+key+"="+strconv.Quote(value))
		escaped = append(escaped, regexp.QuoteMeta(value))
	}
	parts = append(parts, prefix+key+"!~"+strconv.Quote("^("+strings.Join(escaped, "|")+")$"))
	return parts, nil
}

// RepairPartition durably plans ONE explicitly selected hard-limit query. It
// makes no HTTP requests and never resets a hard cap to pending. Stop older
// scanners first. Scan also plans hard limits automatically.
// A second call for the same parent is a no-op, even after inventory changes.
// Cluster, namespace, instance-host and job splits preserve inherited predicates.
// Every root has a total descendant quota, not just a sibling cap.
func (s *ScanStore) RepairPartition(ctx context.Context, parentID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	n, err := repairPartition(ctx, tx, parentID)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

func repairPartition(ctx context.Context, tx *sql.Tx, parentID string) (int, error) {
	if err := ensurePartitionSchema(ctx, tx); err != nil {
		return 0, err
	}
	var inventoryChild int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM namespace_inventory_child WHERE child_id=?`, parentID).Scan(&inventoryChild); err != nil {
		return 0, err
	}
	if inventoryChild != 0 {
		return 0, fmt.Errorf("%w: immutable namespace inventory leaf", errPartitionExhausted)
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM query_partition WHERE parent_id=? AND active=1`, parentID).Scan(&existing); err != nil {
		return 0, err
	}
	if existing > 0 {
		return 0, nil
	}
	var workspace int64
	var ranking int
	var expression, params, kind, metric, grouping, state, class, path string
	var lookback float64
	err := tx.QueryRowContext(ctx, `SELECT q.workspace_id,q.ranking,q.expression,q.params,q.kind,m.display_name,q.grouping,q.state,q.classification,q.path,q.lookback_seconds
 FROM query q JOIN metric m ON m.id=q.metric_id WHERE q.id=? AND q.accepted_attempt_id IS NULL`, parentID).
		Scan(&workspace, &ranking, &expression, &params, &kind, &metric, &grouping, &state, &class, &path, &lookback)
	if err != nil {
		return 0, err
	}
	if state != "blocked" || class != "hard_limit" || path != "/api/v1/query" {
		return 0, errors.New("repair requires a blocked hard-limit instant count query")
	}
	key, predicate, root := "cluster", "", parentID
	var previousKey, ancestor string
	err = tx.QueryRowContext(ctx, `SELECT parent_id,key,child_predicate FROM query_partition WHERE child_id=? AND active=1`, parentID).Scan(&ancestor, &previousKey, &predicate)
	if err == nil {
		key = "namespace"
		if err := tx.QueryRowContext(ctx, `WITH RECURSIVE ancestors(id) AS (
 SELECT ? UNION ALL SELECT l.parent_id FROM query_partition l JOIN ancestors a ON l.child_id=a.id WHERE l.active=1)
 SELECT id FROM ancestors WHERE NOT EXISTS (SELECT 1 FROM query_partition WHERE child_id=ancestors.id AND active=1)`, parentID).Scan(&root); err != nil {
			return 0, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	} else if ranking != 1 {
		return 0, errors.New("repair requires a ranking root or an existing partition child")
	}
	aggregate, duration := "count", "12h"
	switch kind {
	case "run":
		aggregate, duration = "sum", fmt.Sprintf("%dms", int64(lookback*1000))
	case "before12h", "end12h":
		if lookback != 43200 {
			return 0, fmt.Errorf("%w: unexpected active-series lookback", errPartitionExhausted)
		}
	default:
		return 0, fmt.Errorf("%w: unsupported measurement", errPartitionExhausted)
	}
	selector := metric
	if predicate != "" {
		selector += "{" + predicate + "}"
	}
	expected := fmt.Sprintf("%s by(%s)(count_over_time(%s[%s]))", aggregate, grouping, selector, duration)
	v, err := url.ParseQuery(params)
	if err != nil || v.Get("query") != expression || expression != expected || !collectionMetricName.MatchString(metric) {
		return 0, fmt.Errorf("%w: unexpected additive query shape", errPartitionExhausted)
	}
	var parts []string
	var radix []namespacePartition
	phase := ""
	partitionLabel := key
	excludedJSON := "[]"
	var excluded []string
	var literalNode *namespacePartition
	if key == "namespace" {
		var p namespacePartition
		var source *namespacePartition
		err := tx.QueryRowContext(ctx, `SELECT phase,suffix,bucket,depth,terminal FROM fallback_partition WHERE query_id=?`, parentID).Scan(&phase, &p.suffix, &p.bucket, &p.depth, &p.terminal)
		if err == nil {
			if phase == "job" {
				var residual bool
				err := tx.QueryRowContext(ctx, `SELECT excluded_json,residual FROM job_partition WHERE query_id=?`, parentID).Scan(&excludedJSON, &residual)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return 0, err
				}
				if residual {
					partitionLabel = "job"
				} else {
					phase = "instance"
				}
				source = nil
			} else {
				source = &p
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		} else {
			err = tx.QueryRowContext(ctx, `SELECT suffix,bucket,depth,terminal FROM namespace_partition WHERE query_id=?`, parentID).Scan(&p.suffix, &p.bucket, &p.depth, &p.terminal)
			if err == nil {
				source = &p
			} else if !errors.Is(err, sql.ErrNoRows) {
				return 0, err
			}
		}
		// Legacy literal/complement children have no radix metadata. Partition
		// their remaining namespace domain by intersection, preserving the old
		// predicate and all successful siblings rather than replacing the plan.
		radix, err = namespaceParts(source)
		if literal, ok := namespaceLiteral(p); phase == "" && source != nil && ok && literal != "" && !strings.Contains(predicate, "namespace="+strconv.Quote(literal)) {
			parts = []string{predicate + ",namespace=" + strconv.Quote(literal)}
			literalNode, radix, err = &p, nil, nil
		} else if phase == "" && (errors.Is(err, errPartitionExhausted) || (source != nil && source.bucket == "^0-9a-z")) {
			phase, partitionLabel, radix, err = "job", "job", nil, nil
		} else if phase == "instance" && errors.Is(err, errPartitionExhausted) {
			return 0, err
		} else if phase == "instance" && source == nil {
			radix, err = instanceParts(), nil
		} else if phase == "job" {
			radix, err = nil, nil
		}
		if err != nil {
			return 0, err
		}
		for _, part := range radix {
			matcher := part.matcher()
			if phase == "instance" {
				matcher = instanceMatcher(part)
			}
			parts = append(parts, predicate+","+matcher)
		}
	}
	if key == "cluster" || phase == "job" {
		limit := maxPartitionChildren
		if phase == "job" {
			limit = 8
		}
		rows, err := tx.QueryContext(ctx, `SELECT v.value FROM query q
 JOIN observation o ON o.attempt_id=q.accepted_attempt_id
 JOIN labelset_member lm ON lm.labelset_id=o.labelset_id
 JOIN label_name n ON n.id=lm.name_id JOIN label_value v ON v.id=lm.value_id
 WHERE q.workspace_id=? AND q.state='succeeded' AND n.name=?
 AND (?<>'job' OR q.metric_id=(SELECT metric_id FROM query WHERE id=?))
 AND v.value NOT IN (SELECT value FROM json_each(?))
 GROUP BY v.value ORDER BY count(*) DESC,v.value LIMIT ?`, workspace, partitionLabel, partitionLabel, parentID, excludedJSON, limit)
		if err != nil {
			return 0, err
		}
		var values []string
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return 0, err
			}
			values = append(values, value)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return 0, err
		}
		if phase == "job" && len(values) == 0 {
			phase, radix = "instance", instanceParts()
			for _, part := range radix {
				parts = append(parts, predicate+","+instanceMatcher(part))
			}
		} else {
			parts, err = partitionPredicates(predicate, partitionLabel, values)
			if err != nil {
				return 0, err
			}
		}
		if phase == "job" {
			if err := json.Unmarshal([]byte(excludedJSON), &excluded); err != nil {
				return 0, err
			}
			excluded = append(excluded, values...)
			encoded, _ := json.Marshal(excluded)
			excludedJSON = string(encoded)
		}
	}
	var descendants int
	if err := tx.QueryRowContext(ctx, `WITH RECURSIVE descendants(id) AS (
 SELECT child_id FROM query_partition WHERE parent_id=? AND active=1 UNION ALL
 SELECT l.child_id FROM query_partition l JOIN descendants d ON l.parent_id=d.id WHERE l.active=1)
 SELECT count(*) FROM descendants`, root).Scan(&descendants); err != nil {
		return 0, err
	}
	if descendants+len(parts) > maxPartitionChildren {
		return 0, fmt.Errorf("%w: root child quota", errPartitionExhausted)
	}
	for i, part := range parts {
		childExpression := fmt.Sprintf("%s by(%s)(count_over_time(%s{%s}[%s]))", aggregate, grouping, metric, part, duration)
		v.Set("query", childExpression)
		childParams := v.Encode()
		id := scanHash(fmt.Sprintf("%d\n/api/v1/query\n%s", workspace, childParams))
		_, err := tx.ExecContext(ctx, `INSERT INTO query(id,workspace_id,metric_id,window_id,kind,ranking,path,params,expression,evaluation_time,lookback_seconds,grouping,state)
 SELECT ?,workspace_id,metric_id,window_id,kind,0,path,?,?,evaluation_time,lookback_seconds,grouping,'pending' FROM query WHERE id=?
 ON CONFLICT(id) DO NOTHING`, id, childParams, childExpression, parentID)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO query_partition(parent_id,child_id,key,parent_predicate,child_predicate) VALUES(?,?,?,?,?)
 ON CONFLICT(parent_id,child_id) DO UPDATE SET active=1`, parentID, id, key, predicate, part); err != nil {
			return 0, err
		}
		// A superseded unexecuted query can be reused. Never reset hard failures
		// or accepted results; retained failures can only gain a different plan.
		if _, err := tx.ExecContext(ctx, `UPDATE query SET state=CASE WHEN attempt_count=0 THEN 'pending' ELSE 'blocked' END,
 classification=CASE WHEN attempt_count=0 THEN '' ELSE 'hard_limit' END WHERE id=? AND classification='superseded' AND accepted_attempt_id IS NULL`, id); err != nil {
			return 0, err
		}
		if phase != "" {
			var p namespacePartition
			if phase == "instance" {
				p = radix[i]
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO fallback_partition(query_id,phase,suffix,bucket,depth,terminal) VALUES(?,?,?,?,?,?) ON CONFLICT(query_id) DO NOTHING`, id, phase, p.suffix, p.bucket, p.depth, p.terminal); err != nil {
				return 0, err
			}
			if phase == "job" {
				if _, err := tx.ExecContext(ctx, `INSERT INTO job_partition(query_id,excluded_json,residual) VALUES(?,?,?) ON CONFLICT(query_id) DO NOTHING`, id, excludedJSON, i == len(parts)-1); err != nil {
					return 0, err
				}
			}
		} else if key == "namespace" {
			var p namespacePartition
			if literalNode != nil {
				p = *literalNode
			} else {
				p = radix[i]
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO namespace_partition(query_id,suffix,bucket,depth,terminal) VALUES(?,?,?,?,?) ON CONFLICT(query_id) DO NOTHING`, id, p.suffix, p.bucket, p.depth, p.terminal); err != nil {
				return 0, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE query SET classification='partition_pending' WHERE id=?`, parentID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE run SET state='active' WHERE id=1`); err != nil {
		return 0, err
	}
	return len(parts), nil
}

// Supersede only unfinished namespace boundaries. Descendant queries/attempts
// remain audit evidence; active edges alone describe the new exhaustive plan.
// Refuse leased work: an older scanner must not publish into a replaced plan.
func supersedeTerminalPartitions(ctx context.Context, tx *sql.Tx) error {
	var running int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM query WHERE state='running'`).Scan(&running); err != nil {
		return err
	}
	if running != 0 {
		return errors.New("partition v4 upgrade requires stopped scanners with no running leases")
	}
	rows, err := tx.QueryContext(ctx, `SELECT q.id,n.suffix,n.bucket,n.depth,n.terminal FROM namespace_partition n
 JOIN query q ON q.id=n.query_id WHERE n.terminal=1 AND q.state='blocked' AND q.accepted_attempt_id IS NULL
 AND q.classification IN ('hard_limit','partition_pending','partition_exhausted')
 AND EXISTS(SELECT 1 FROM query_partition l WHERE l.child_id=q.id AND l.active=1)`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		var p namespacePartition
		if err := rows.Scan(&id, &p.suffix, &p.bucket, &p.depth, &p.terminal); err != nil {
			rows.Close()
			return err
		}
		if _, ok := namespaceLiteral(p); ok {
			ids = append(ids, id)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		// Mark every edge below the boundary inactive, including deeper edges.
		if _, err := tx.ExecContext(ctx, `WITH RECURSIVE d(id) AS (
 SELECT child_id FROM query_partition WHERE parent_id=? AND active=1 UNION ALL
 SELECT l.child_id FROM query_partition l JOIN d ON l.parent_id=d.id WHERE l.active=1)
 UPDATE query_partition SET active=0 WHERE child_id IN (SELECT id FROM d)`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE query SET state='blocked',classification='superseded',owner=NULL,lease_until=NULL,current_attempt_id=NULL
 WHERE accepted_attempt_id IS NULL AND id IN (SELECT child_id FROM query_partition WHERE active=0)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE query SET classification='hard_limit' WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// Called in the claim transaction as well as the planner so requests below a
// failed root cannot slip through between separate planning and claim commits.
func blockInactivePartitionWork(ctx context.Context, tx *sql.Tx) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name='query_partition' AND type='table'`).Scan(&exists); err != nil || exists == 0 {
		return err
	}
	_, err := tx.ExecContext(ctx, `WITH RECURSIVE disabled(id) AS (
 SELECT l.child_id FROM query_partition l JOIN query p ON p.id=l.parent_id
 WHERE l.active=0 OR (p.state='blocked' AND p.classification NOT IN ('hard_limit','partition_pending','namespace_inventory_pending'))
 UNION SELECT l.child_id FROM query_partition l JOIN disabled d ON l.parent_id=d.id WHERE l.active=1)
 UPDATE query SET state='blocked',classification='partition_inactive' WHERE state IN ('pending','retry_wait') AND id IN (SELECT id FROM disabled)`)
	return err
}

// planHardLimitPartitions runs before claimScan, in its own immediate transaction.
// Namespace refinement does not depend on cluster siblings or observed namespace
// inventories. Instance-host/job fallback resolves terminal namespace leaves.
// A versioned transition reopens old exhaustion once, never resetting requests or
// successful evidence. New exhausted plans stay terminal.
func (s *ScanStore) planHardLimitPartitions(ctx context.Context) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensurePartitionSchema(ctx, tx); err != nil {
		return false, err
	}
	if err := finalizeNamespaceInventory(ctx, tx); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO partition_version(version) VALUES(4) ON CONFLICT DO NOTHING`)
	if err != nil {
		return false, err
	}
	versionAdded, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if versionAdded > 0 {
		if err := supersedeTerminalPartitions(ctx, tx); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE query SET classification=CASE
 WHEN EXISTS (SELECT 1 FROM query_partition l WHERE l.parent_id=query.id AND l.active=1) THEN 'partition_pending' ELSE 'hard_limit' END
 WHERE state='blocked' AND accepted_attempt_id IS NULL AND classification IN ('partition_exhausted','partition_filter_ineffective','partition_inactive')
 AND (ranking=1 OR EXISTS (SELECT 1 FROM query_partition l WHERE l.child_id=query.id AND l.active=1))`); err != nil {
			return false, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT q.id FROM query q WHERE q.state='blocked' AND q.classification='hard_limit'
 AND q.path='/api/v1/query' AND (q.ranking=1 OR EXISTS (SELECT 1 FROM query_partition l WHERE l.child_id=q.id AND l.active=1))
 ORDER BY EXISTS(SELECT 1 FROM namespace_partition n WHERE n.query_id=q.id AND n.terminal=1) DESC,q.rowid`)
	if err != nil {
		return false, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, err
	}
	planned := false
	for _, id := range ids {
		var parent, key string
		err := tx.QueryRowContext(ctx, `SELECT parent_id,key FROM query_partition WHERE child_id=? AND active=1`, id).Scan(&parent, &key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		classification := "partition_exhausted"
		if parent != "" {
			var terminal bool
			if err := tx.QueryRowContext(ctx, `SELECT classification<>'partition_pending' FROM query WHERE id=?`, parent).Scan(&terminal); err != nil {
				return false, err
			}
			if terminal {
				if _, err := tx.ExecContext(ctx, `UPDATE query SET classification='partition_exhausted' WHERE id=?`, id); err != nil {
					return false, err
				}
				continue
			}
		}
		n, err := repairPartition(ctx, tx, id)
		if err == nil {
			planned = planned || n > 0
			continue
		}
		if !errors.Is(err, errPartitionExhausted) {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE query SET classification=? WHERE id=?`, classification, id); err != nil {
			return false, err
		}
	}
	// Propagate permanent failure up the tree, but never publish partial data.
	for {
		res, err := tx.ExecContext(ctx, `UPDATE query SET classification='partition_exhausted'
 WHERE state='blocked' AND classification='partition_pending' AND EXISTS (
 SELECT 1 FROM query_partition l JOIN query c ON c.id=l.child_id WHERE l.parent_id=query.id AND l.active=1
 AND c.state='blocked' AND c.classification NOT IN ('hard_limit','partition_pending'))`)
		if err != nil {
			return false, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		if n == 0 {
			break
		}
	}
	if err := blockInactivePartitionWork(ctx, tx); err != nil {
		return false, err
	}
	return planned, tx.Commit()
}

// Called inside the claim transaction, before deciding scheduling is complete.
// The table is initialized by the planner, not by read-only status/render paths.
func finalizeReadyPartitions(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if err := finalizeNamespaceInventory(ctx, tx); err != nil {
		return err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='query_partition'`).Scan(&exists); err != nil || exists == 0 {
		return err
	}
	for {
		var parent string
		err := tx.QueryRowContext(ctx, `SELECT p.id FROM query p WHERE p.state='blocked' AND p.classification IN ('partition_pending','namespace_inventory_pending')
 AND EXISTS (SELECT 1 FROM query_partition l WHERE l.parent_id=p.id AND l.active=1)
 AND NOT EXISTS (SELECT 1 FROM query_partition l JOIN query c ON c.id=l.child_id
 WHERE l.parent_id=p.id AND l.active=1 AND (c.state<>'succeeded' OR c.accepted_attempt_id IS NULL)) LIMIT 1`).Scan(&parent)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		// Invalid counts never turn into zero or disappear in SUM. A partial or
		// invalid partition is unknown, even when every HTTP request succeeded.
		var invalid int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM query_partition l JOIN query c ON c.id=l.child_id
 JOIN attempt a ON a.id=c.accepted_attempt_id LEFT JOIN observation o ON o.attempt_id=a.id
 WHERE l.parent_id=? AND l.active=1 AND (a.state<>'succeeded' OR a.warnings_json NOT IN ('[]','null') OR
 (o.attempt_id IS NOT NULL AND (o.invalid_value IS NOT NULL OR o.integer_value IS NULL OR o.integer_value<0 OR o.value IS NULL OR o.timestamp<>c.evaluation_time)))`, parent).Scan(&invalid); err != nil {
			return err
		}
		if invalid > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE query SET classification='partition_invalid' WHERE id=?`, parent); err != nil {
				return err
			}
			continue
		}
		// Synthetic attempts have no HTTP/cost/duration: child attempts retain
		// actual request statistics, and links retain all provenance recursively.
		res, err := tx.ExecContext(ctx, `INSERT INTO attempt(query_id,owner,started_at,finished_at,state,classification,stats_json)
 VALUES(?,'repair',?,?,'succeeded','partitioned',json_object('source','query_partition','children',(SELECT count(*) FROM query_partition WHERE parent_id=? AND active=1)))`, parent, scanTime(now), scanTime(now), parent)
		if err != nil {
			return err
		}
		attempt, err := res.LastInsertId()
		if err != nil {
			return err
		}
		// The partition label need not occur in the grouping. Sum identical
		// groups across disjoint underlying series, not distinct grouped rows.
		_, err = tx.ExecContext(ctx, `INSERT INTO observation(attempt_id,labelset_id,timestamp,value,integer_value)
 SELECT ?,o.labelset_id,o.timestamp,sum(o.integer_value),sum(o.integer_value)
 FROM query_partition l JOIN query c ON c.id=l.child_id JOIN observation o ON o.attempt_id=c.accepted_attempt_id
 WHERE l.parent_id=? AND l.active=1 GROUP BY o.labelset_id,o.timestamp`, attempt, parent)
		if err != nil {
			if strings.Contains(err.Error(), "integer overflow") {
				if _, err := tx.ExecContext(ctx, `DELETE FROM attempt WHERE id=?`, attempt); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE query SET classification='partition_overflow' WHERE id=?`, parent); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE query SET state='succeeded',classification='partitioned',accepted_attempt_id=?,attempt_count=attempt_count+1,
 first_attempt_at=coalesce(first_attempt_at,?),owner=NULL,lease_until=NULL,current_attempt_id=NULL,retry_at=0 WHERE id=?`, attempt, scanTime(now), parent); err != nil {
			return err
		}
	}
}
