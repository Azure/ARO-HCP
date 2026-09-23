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
	"sort"
	"time"
)

const namespaceInventoryBudget = 1500

const namespaceInventorySchema = `
CREATE TABLE IF NOT EXISTS namespace_inventory_plan (
 root_id TEXT PRIMARY KEY REFERENCES query(id), candidate_snapshot_hash TEXT NOT NULL,
 candidates_json TEXT NOT NULL, child_count INTEGER NOT NULL,
 attempt_baseline INTEGER NOT NULL, created_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS namespace_inventory_child (
 root_id TEXT NOT NULL REFERENCES namespace_inventory_plan(root_id),
 child_id TEXT NOT NULL UNIQUE REFERENCES query(id),
 child_role TEXT NOT NULL CHECK(child_role IN ('literal','namespace_complement','cluster_complement')),
 predicate TEXT NOT NULL, PRIMARY KEY(root_id,child_id)
);
CREATE TABLE IF NOT EXISTS namespace_inventory_edge_history (
 parent_id TEXT NOT NULL, child_id TEXT NOT NULL, key TEXT NOT NULL,
 parent_predicate TEXT NOT NULL, child_predicate TEXT NOT NULL,
 PRIMARY KEY(parent_id,child_id)
);`

type namespaceCandidate struct {
	Cluster, Namespace, SourceQuery                                   string
	ClusterValueID, NamespaceValueID, LabelsetID, AttemptID, WindowID int64
	WindowKind                                                        string
	Start, End                                                        float64
}

// PlanNamespaceRecovery replaces all unfinished additive ranking roots once, in
// one transaction. Inventory is a hint, not proof of completeness: complements
// always cover unseen, empty and absent labels. No requests are made here.
func (s *ScanStore) PlanNamespaceRecovery(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := ensurePartitionSchema(ctx, tx); err != nil {
		return 0, err
	}
	var running int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM query WHERE state='running'`).Scan(&running); err != nil {
		return 0, err
	}
	if running != 0 {
		return 0, errors.New("namespace recovery requires stopped scanners with no running leases")
	}
	type root struct {
		id, metric, kind, expression, params, grouping string
		workspace                                      int64
		lookback                                       float64
	}
	rows, err := tx.QueryContext(ctx, `SELECT q.id,q.workspace_id,m.display_name,q.kind,q.expression,q.params,q.grouping,q.lookback_seconds
 FROM query q JOIN metric m ON m.id=q.metric_id WHERE q.ranking=1 AND q.state='blocked' AND q.accepted_attempt_id IS NULL
 AND q.classification IN ('hard_limit','partition_pending','partition_exhausted','partition_filter_ineffective','waiting_inventory')
 AND NOT EXISTS(SELECT 1 FROM namespace_inventory_plan p WHERE p.root_id=q.id) ORDER BY q.rowid`)
	if err != nil {
		return 0, err
	}
	var roots []root
	for rows.Next() {
		var r root
		if err := rows.Scan(&r.id, &r.workspace, &r.metric, &r.kind, &r.expression, &r.params, &r.grouping, &r.lookback); err != nil {
			rows.Close()
			return 0, err
		}
		roots = append(roots, r)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	if len(roots) == 0 {
		return 0, tx.Commit()
	}
	var baseline int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(id),0) FROM attempt`).Scan(&baseline); err != nil {
		return 0, err
	}
	total := 0
	var previousChildren int
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(child_count),0) FROM namespace_inventory_plan`).Scan(&previousChildren); err != nil {
		return 0, err
	}
	for _, r := range roots {
		aggregate, duration := "count", "12h"
		switch r.kind {
		case "run":
			aggregate, duration = "sum", fmt.Sprintf("%dms", int64(r.lookback*1000))
		case "before12h", "end12h":
			if r.lookback != 43200 {
				return 0, errors.New("unexpected namespace recovery lookback")
			}
		default:
			return 0, fmt.Errorf("unsupported namespace recovery kind %q", r.kind)
		}
		expected := fmt.Sprintf("%s by(%s)(count_over_time(%s[%s]))", aggregate, r.grouping, r.metric, duration)
		params, err := url.ParseQuery(r.params)
		if err != nil || r.expression != expected || params.Get("query") != expected || !collectionMetricName.MatchString(r.metric) {
			return 0, errors.New("unexpected namespace recovery query shape")
		}
		// Join labels on the same normalized labelset, never a cluster/namespace
		// cross product. Only accepted ranking counts in the actual 12h windows.
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT cv.value,nv.value,q.id,cv.id,nv.id,o.labelset_id,q.accepted_attempt_id,w.id,w.kind,w.start,w.end
 FROM query q JOIN metric m ON m.id=q.metric_id JOIN window w ON w.id=q.window_id JOIN run r ON r.id=w.run_id
 JOIN attempt a ON a.id=q.accepted_attempt_id JOIN observation o ON o.attempt_id=a.id
 JOIN labelset_member cm ON cm.labelset_id=o.labelset_id JOIN label_name cn ON cn.id=cm.name_id AND cn.name='cluster' JOIN label_value cv ON cv.id=cm.value_id
 JOIN labelset_member nm ON nm.labelset_id=o.labelset_id JOIN label_name nn ON nn.id=nm.name_id AND nn.name='namespace' JOIN label_value nv ON nv.id=nm.value_id
 WHERE q.workspace_id=? AND q.ranking=1 AND q.state='succeeded' AND a.state='succeeded' AND a.warnings_json IN ('[]','null')
 AND m.name IN ('kube_namespace_created','kube_pod_info','up') AND q.kind=w.kind AND w.kind IN ('before12h','end12h')
 AND q.lookback_seconds=43200 AND q.evaluation_time=w.end AND w.start=w.end-43200
 AND w.end=CASE w.kind WHEN 'before12h' THEN r.start ELSE r.end END AND o.timestamp=w.end
 AND o.invalid_value IS NULL AND o.integer_value>0 AND cv.value<>'' AND nv.value<>''
 ORDER BY cv.value,nv.value,q.id,o.labelset_id`, r.workspace)
		if err != nil {
			return 0, err
		}
		candidates := []namespaceCandidate{}
		pairs := map[string]map[string]bool{}
		for rows.Next() {
			var c namespaceCandidate
			if err := rows.Scan(&c.Cluster, &c.Namespace, &c.SourceQuery, &c.ClusterValueID, &c.NamespaceValueID, &c.LabelsetID, &c.AttemptID, &c.WindowID, &c.WindowKind, &c.Start, &c.End); err != nil {
				rows.Close()
				return 0, err
			}
			candidates = append(candidates, c)
			if pairs[c.Cluster] == nil {
				pairs[c.Cluster] = map[string]bool{}
			}
			pairs[c.Cluster][c.Namespace] = true
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return 0, err
		}
		if len(candidates) == 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE query SET classification='waiting_inventory' WHERE id=?`, r.id); err != nil {
				return 0, err
			}
			continue
		}
		clusters := make([]string, 0, len(pairs))
		for cluster := range pairs {
			clusters = append(clusters, cluster)
		}
		sort.Strings(clusters)
		clusterParts, err := partitionPredicates("", "cluster", clusters)
		if err != nil {
			return 0, err
		}
		type child struct{ predicate, role string }
		var literals, remainders []child
		for i, cluster := range clusters {
			names := make([]string, 0, len(pairs[cluster]))
			for ns := range pairs[cluster] {
				names = append(names, ns)
			}
			parts, err := partitionPredicates(clusterParts[i], "namespace", names)
			if err != nil {
				return 0, err
			}
			for _, p := range parts[:len(parts)-1] {
				literals = append(literals, child{p, "literal"})
			}
			remainders = append(remainders, child{parts[len(parts)-1], "namespace_complement"})
		}
		children := append(literals, remainders...)
		children = append(children, child{clusterParts[len(clusterParts)-1], "cluster_complement"})
		total += len(children)
		if previousChildren+total > namespaceInventoryBudget {
			return 0, fmt.Errorf("namespace inventory exceeds %d request budget", namespaceInventoryBudget)
		}
		encoded, _ := json.Marshal(candidates)
		if _, err := tx.ExecContext(ctx, `INSERT INTO namespace_inventory_plan VALUES(?,?,?,?,?,?)`, r.id, scanHash(string(encoded)), string(encoded), len(children), baseline, scanTime(time.Now())); err != nil {
			return 0, err
		}
		// Keep all old queries and attempts, but remove every old edge from the
		// active coverage graph before adding the direct exhaustive replacement.
		if _, err := tx.ExecContext(ctx, `WITH RECURSIVE d(id) AS (SELECT child_id FROM query_partition WHERE parent_id=? UNION SELECT l.child_id FROM query_partition l JOIN d ON l.parent_id=d.id)
 UPDATE query_partition SET active=0 WHERE child_id IN (SELECT id FROM d)`, r.id); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE query SET state='blocked',classification='superseded',owner=NULL,lease_until=NULL,current_attempt_id=NULL
 WHERE accepted_attempt_id IS NULL AND id IN (SELECT child_id FROM query_partition WHERE active=0) AND state IN ('pending','retry_wait')`); err != nil {
			return 0, err
		}
		for _, c := range children {
			expression := fmt.Sprintf("%s by(%s)(count_over_time(%s{%s}[%s]))", aggregate, r.grouping, r.metric, c.predicate, duration)
			params.Set("query", expression)
			encodedParams := params.Encode()
			id := scanHash(fmt.Sprintf("%d\n/api/v1/query\n%s", r.workspace, encodedParams))
			var reused string
			err := tx.QueryRowContext(ctx, `SELECT id FROM query WHERE workspace_id=? AND path='/api/v1/query' AND params=?`, r.workspace, encodedParams).Scan(&reused)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return 0, err
			}
			if reused != "" {
				id = reused
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO query(id,workspace_id,metric_id,window_id,kind,ranking,path,params,expression,evaluation_time,lookback_seconds,grouping,state)
 SELECT ?,workspace_id,metric_id,window_id,kind,0,path,?,?,evaluation_time,lookback_seconds,grouping,'pending' FROM query WHERE id=? ON CONFLICT(id) DO NOTHING`, id, encodedParams, expression, r.id); err != nil {
				return 0, err
			}
			var oldParent string
			var active int
			err = tx.QueryRowContext(ctx, `SELECT parent_id,active FROM query_partition WHERE child_id=?`, id).Scan(&oldParent, &active)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return 0, err
			}
			if oldParent != "" && oldParent != r.id {
				if active != 0 {
					return 0, errors.New("namespace child belongs to another active plan")
				}
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO namespace_inventory_edge_history SELECT parent_id,child_id,key,parent_predicate,child_predicate FROM query_partition WHERE child_id=?`, id); err != nil {
					return 0, err
				}
			}
			key := "namespace"
			if c.role == "cluster_complement" {
				key = "cluster"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO query_partition(parent_id,child_id,key,parent_predicate,child_predicate,active) VALUES(?,?,?,'',?,1)
 ON CONFLICT(child_id) DO UPDATE SET parent_id=excluded.parent_id,key=excluded.key,parent_predicate='',child_predicate=excluded.child_predicate,active=1`, r.id, id, key, c.predicate); err != nil {
				return 0, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO namespace_inventory_child VALUES(?,?,?,?)`, r.id, id, c.role, c.predicate); err != nil {
				return 0, err
			}
			// Reuse accepted results and terminal failures unchanged. Only an old
			// unattempted query may become pending again; never retry a remainder.
			if _, err := tx.ExecContext(ctx, `UPDATE query SET state='pending',classification='' WHERE id=? AND accepted_attempt_id IS NULL AND attempt_count=0
 AND classification IN ('superseded','partition_inactive')`, id); err != nil {
				return 0, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE query SET classification='namespace_inventory_pending' WHERE id=?`, r.id); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE run SET state='active' WHERE id=1 AND EXISTS(SELECT 1 FROM query WHERE state IN ('pending','running','retry_wait'))`); err != nil {
		return 0, err
	}
	return total, tx.Commit()
}

// Failed children are terminal, never radix inputs. Wait for ALL siblings before
// failing a root, retaining active edges for disjoint leaf lower-bound evidence.
func finalizeNamespaceInventory(ctx context.Context, tx *sql.Tx) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name='namespace_inventory_plan'`).Scan(&exists); err != nil || exists == 0 {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE query SET classification='namespace_inventory_limit' WHERE state='blocked' AND classification='hard_limit' AND id IN (SELECT child_id FROM namespace_inventory_child);
 UPDATE query SET classification='namespace_inventory_incomplete' WHERE classification='namespace_inventory_pending'
 AND NOT EXISTS (SELECT 1 FROM namespace_inventory_child l JOIN query c ON c.id=l.child_id WHERE l.root_id=query.id AND c.state IN ('pending','running','retry_wait'))
 AND EXISTS (SELECT 1 FROM namespace_inventory_child l JOIN query c ON c.id=l.child_id WHERE l.root_id=query.id AND (c.state<>'succeeded' OR c.accepted_attempt_id IS NULL));`)
	return err
}
