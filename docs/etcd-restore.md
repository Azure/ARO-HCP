# SOP — Restore a HyperShift (ARO-HCP) etcd cluster from a Velero backup

**What this does:** rebuilds a Managed 3-member etcd cluster for a HostedControlPlane
from a Velero backup, using one canonical snapshot restored identically
into all three PVCs (so all three boot as ONE `state=new` cluster).

**When to use:** etcd data loss / split-brain / a member restored single-node, and
you need a clean HA etcd from a known-good backup.

---

## CAUTION

1. **Do NOT grow the StatefulSet 1→2→3.** The HyperShift maintained etcd statefulset hardcodes a 3-member
   `ETCD_INITIAL_CLUSTER`; etcd 3.6 rejects an `existing` join whenever real
   membership ≠ 3 (`member count is unequal`), and `reset-member` re-adds as a
   voter, killing quorum. Restore all three together instead.
2. **The three per-pod `backup.db` files are usually DIFFERENT snapshots** (the
   Velero pre-hook fires per-pod at slightly different instants). Copy the freshest
   (highest revision) to all three — the copy is mandatory.
3. This process restores **etcd only**.

### Rollback / safety notes

- All work happens while the HC is **paused** and KAS is **scaled to 0**; nothing
  serves traffic mid-procedure.
- The GATES in steps 7e and 8 are the guardrails against the split-brain failure
  mode (mismatched snapshots or mismatched cluster-ids). Do not skip them.
- If a GATE fails, delete the holder/restore pods and re-run from step 7 — the PVC
  data dirs are recreated by the restore jobs, so retries are safe.

---
# Backups

## 0. Parameters
```bash
export KUBECONFIG=/path/to/mgmt.kubeconfig
export HC_NS=<hostedcluster namespace>     # where the HostedCluster lives, e.g. ocm-...
export HC=<hostedcluster name>             # e.g. f3e7i2o6r4k9b0q
export HCP_NS=<control-plane namespace>    # etcd namespace = ${HC_NS}-${HC}
export TS=$(date +%Y%m%d%H%M%S)
```

## 1. Taking a backup

Backups are taken on a schedule, at least hourly.
```bash
# List backup schedules
kubectl get schedules -n velero | grep "${HC_NS}"
```

You can take an on-demand backup from a schedule with the velero cli.
```bash
# Fetch velero server pod
kubectl get pods -n velero -l deploy=velero

# Exec into the pod to access velero cli
kubectl exec -it -n velero <velero_pod_name> -- /bin/sh

# The velero binary can then be accessed, use it to take a backup using the backup definition from a schedule
./velero backup create --from-schedule <schedule_name>
```

You can check the status of an on-demand backup by either inspecting the status field directly or with `velero backup describe`
Ensure the backup is Completed and no pre/post hooks failed
```bash
# Exec into the pod to access velero cli and describe the backup
./velero backup describe <backup_name>
```
Example output:
```bash
Started:    2026-08-24 18:09:09 +0000 UTC
Completed:  2026-08-24 18:11:29 +0000 UTC

Expiration:  2026-08-31 18:09:09 +0000 UTC

Total items to be backed up:  385
Items backed up:              385

Backup Item Operations:  3 of 3 completed successfully, 0 failed (specify --details for more information)
Backup Volumes:
  Velero-Native Snapshots: <none included>

  CSI Snapshots:
    ocm-arohcppers-2sd1pej7kdkk1qvccoiqq534ae1knhh4-b0u3e9c9h9v3t4y/data-etcd-2:
      Data Movement: included, specify --details for more information
    ocm-arohcppers-2sd1pej7kdkk1qvccoiqq534ae1knhh4-b0u3e9c9h9v3t4y/data-etcd-0:
      Data Movement: included, specify --details for more information
    ocm-arohcppers-2sd1pej7kdkk1qvccoiqq534ae1knhh4-b0u3e9c9h9v3t4y/data-etcd-1:
      Data Movement: included, specify --details for more information

  Pod Volume Backups: <none included>

HooksAttempted:  6
HooksFailed:     0
```

## 2. Identify a valid backup

Identifying the proper backup is a two step process.
1. Identify when the problem that requires recovery first occurred.
2. Select a backup that was successfully taken just before the start of the issue.


```bash
# List available backups for this cluster
kubectl get backup -n velero | grep "${HC_NS}"

# Backups created from a schedule are named <schedule-name>-<YYYYMMDDHHMMSS>, where
# <schedule-name> is ${HC_NS}-<hourly|daily|weekly>. For example:
#   ocm-arohcppers-2sd1pej7kdkk1qvccoiqq534ae1knhh4-hourly-20260824180909

# list backups by creationTimestamp
kubectl get backup -n velero --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[?(@.status.phase=="Completed")].metadata.name}'

# record the chosen backup for the rest of the procedure
export BACKUP_NAME=<velero backup name>

# determine a viable backup by ensuring status.phase is Completed
kubectl get backup ${BACKUP_NAME} -n velero -o jsonpath='{.status.phase}'
```

**GATE — confirm the backup belongs to THIS cluster.** The Backup and the HostedCluster
both carry the cluster's Azure resource ID in the
`azure.microsoft.com/hcp-cluster-azure-resource-id` annotation.

```bash
# resource ID on the chosen backup
kubectl get backup ${BACKUP_NAME} -n velero \
  -o jsonpath='{.metadata.annotations.azure\.microsoft\.com/hcp-cluster-azure-resource-id}{"\n"}'

# resource ID on the HostedCluster being restored
kubectl get hostedcluster ${HC} -n ${HC_NS} \
  -o jsonpath='{.metadata.annotations.azure\.microsoft\.com/hcp-cluster-azure-resource-id}{"\n"}'
```


# ETCD Restore
## 0. Parameters — set once per shell

```bash
export KUBECONFIG=/path/to/mgmt.kubeconfig
export HC_NS=<hostedcluster namespace>     # where the HostedCluster lives, e.g. ocm-...
export HC=<hostedcluster name>             # e.g. f3e7i2o6r4k9b0q
export HCP_NS=<control-plane namespace>    # etcd namespace = ${HC_NS}-${HC}
export BACKUP_NAME=<velero backup name>    # the velero backup to restore from
export TS=$(date +%Y%m%d%H%M%S)
```

```bash
# etcd image (has etcdutl/etcdctl + tar)
# pod identity used by the etcd pod (match it so restored files are owned correctly)

export ETCD_IMG=$(kubectl -n $HCP_NS get statefulset etcd \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="etcd")].image}')
export ETCD_UID=$(kubectl -n $HCP_NS get statefulset etcd -o jsonpath='{.spec.template.spec.securityContext.runAsUser}')
export ETCD_GID=$(kubectl -n $HCP_NS get statefulset etcd -o jsonpath='{.spec.template.spec.securityContext.fsGroup}')
echo "ETCD_IMG=$ETCD_IMG ETCD_UID=$ETCD_UID ETCD_GID=$ETCD_GID"
```
---

## 1. Pause backup schedules for the cluster

Pause the cluster's backup schedules so a scheduled backup cannot fire mid-restore.
Backup scheduling is controlled per-cluster through the admin API, which sets
`ServiceProviderCluster.Spec.BackupScheduleState` and propagates to the Velero Schedule's
`spec.paused` on the next backend reconcile. In MSFT environments the admin API is the
surface intended to be exposed as Geneva Actions (not yet wired up — see
[backups.md](backups.md#admin-api-reference); currently reached via direct HTTP to the
admin service).

### Dev environments (direct HTTP)

The admin API runs on the **service cluster** (service `admin-api` in namespace
`aro-hcp-admin-api`, port 8443), not the management cluster. It is not exposed externally;
reach it with a port-forward using the service-cluster kubeconfig:

```bash
kubectl --kubeconfig=/path/to/svc.kubeconfig -n aro-hcp-admin-api \
  port-forward svc/admin-api 8443:8443
```

The `X-Ms-Client-Principal-*` headers are injected by Geneva Actions in MSFT. For a direct
call you must supply them yourself — the values below are placeholders, but the headers are
required (the request returns 401 without `X-Ms-Client-Principal-Name`).

```bash
export ADMIN_API=http://localhost:8443              # via the port-forward above
export SUB=<subscription-id>
export RG=<resource-group>
export CLUSTER_NAME=<arm cluster name>
export BACKUP_SCHEDULES="$ADMIN_API/admin/v1/hcp/subscriptions/$SUB/resourcegroups/$RG/providers/microsoft.redhatopenshift/hcpopenshiftclusters/$CLUSTER_NAME/backupschedules"
```

Pause:
```bash
curl -s -X PATCH "$BACKUP_SCHEDULES" \
  -H 'X-Ms-Client-Principal-Name: localtest@redhat.com' \
  -H 'X-Ms-Client-Principal-Type: dstsUser' \
  -H 'Content-Type: application/json' \
  -d '{"state": "Disabled"}'
```

Verify the schedules report paused before continuing. Either query the admin API (each
schedule should show `"backupExecutionState": "Paused"`):
```bash
curl -s "$BACKUP_SCHEDULES" \
  -H 'X-Ms-Client-Principal-Name: localtest@redhat.com' \
  -H 'X-Ms-Client-Principal-Type: dstsUser' | jq
```

...or, with management-cluster access, check the Velero Schedules directly and confirm the
`PAUSED` column is `true` for every schedule:
```bash
kubectl get schedules -n velero | grep "${HC_NS}"
```
```
NAME                                                     STATUS    SCHEDULE      LASTBACKUP   AGE   PAUSED
ocm-arohcppers-2sd1pej7kdkk1qvccoiqq534ae1knhh4-daily    Enabled   0 2 * * *                  36m   true
ocm-arohcppers-2sd1pej7kdkk1qvccoiqq534ae1knhh4-hourly   Enabled   0 */1 * * *                36m   true
ocm-arohcppers-2sd1pej7kdkk1qvccoiqq534ae1knhh4-weekly   Enabled   0 3 * * 0                  36m   true
```

### MSFT environments (Geneva Action)

In MSFT environments use the Geneva Action:

> **Azure Red Hat Hypershift > Fleet Operations > Update Backup Schedule**

Enter the **HCP Resource ID** and set **Schedule State** to `Disabled`.

Verify with the companion lookup Geneva Action, supplying the same **HCP Resource ID**:

> **Azure Red Hat Hypershift > Fleet Operations > Get Backup Schedule**

The response carries per-schedule detail; every schedule should report
`"backupExecutionState": "Paused"`:
```json
{
  "state": "Disabled",
  "schedules": [
    {"name": "...-hourly", "lastBackupTime": "2026-05-27T02:00:15Z", "phase": "Enabled", "backupExecutionState": "Paused"},
    {"name": "...-daily",  "lastBackupTime": "2026-05-27T02:00:00Z", "phase": "Enabled", "backupExecutionState": "Paused"},
    {"name": "...-weekly", "lastBackupTime": "2026-05-25T03:00:00Z", "phase": "Enabled", "backupExecutionState": "Paused"}
  ]
}
```

---

Re-enable the schedules after the restore is complete — see
[step 12](#12-re-enable-backup-schedules). See [backups.md](backups.md) for the full backup
and schedule design.

## 2. Pause the HostedCluster to stop CPO from re-scaling the api server and etcd.

```bash
kubectl -n $HC_NS patch hostedcluster/$HC --type=merge \
  -p '{"spec":{"pausedUntil":"true"}}'
kubectl -n $HC_NS get hostedcluster/$HC -o jsonpath='pausedUntil={.spec.pausedUntil}{"\n"}'
```

## 3. Scale etcd and KAS to 0

```bash
kubectl -n $HCP_NS scale statefulset/etcd --replicas=0
kubectl -n $HCP_NS scale deployment/kube-apiserver --replicas=0
kubectl -n $HCP_NS wait --for=delete pod -l app=kube-apiserver --timeout=5m
kubectl -n $HCP_NS wait --for=delete pod -l app=etcd --timeout=5m
```

## 4. Delete the three etcd PVCs

Delete PVCs prior to restore

```bash
kubectl -n $HCP_NS delete pvc data-etcd-0 data-etcd-1 data-etcd-2 --wait=true
```

## 5. Restore all three etcd PVCs from the velero backup

PVCs are labeled `app=etcd` only (no per-member label), so restore all three.

The etcd StatefulSet is restored because the `data-etcd-*` PersistentVolumeClaims use a
WaitForFirstConsumer storage class: the PersistentVolume is not provisioned, and the
volume snapshot not restored, until a pod mounts the claim. The StatefulSet pods are that
first consumer, so the StatefulSet must be restored to trigger binding and restore.
NOTE: Match labels does not work for the StatefulSet, there is nothing to match on.

> **Reminder:** `$HC`, `$TS`, and `$BACKUP_NAME` were exported in
> [§0 Parameters](#0-parameters--set-once-per-shell) at the start of this procedure —
> `$TS` is the timestamp captured then. Re-run
> `echo "HC=$HC TS=$TS BACKUP_NAME=$BACKUP_NAME"` to confirm they are still set in your
> current shell before applying the Restore.

```bash
export RESTORE_NAME=restore-$HC-$TS

cat <<EOF | kubectl apply -f -
apiVersion: velero.io/v1
kind: Restore
metadata:
  name: $RESTORE_NAME
  namespace: velero
spec:
  backupName: $BACKUP_NAME
  restorePVs: true
  existingResourcePolicy: update
  includedResources:
  - pv
  - pvc
  - statefulsets
  itemOperationTimeout: 4h0m0s
EOF

# Monitor the status
kubectl  get restore $RESTORE_NAME -n velero -ojsonpath='{.status}' | jq
# confirm etcd PVCs are Bound
kubectl  -n $HCP_NS get pvc -l app=etcd
```

## 6. Scale etcd down again after restore

Restoring the StatefulSet sets its replicas back to 3, so etcd pods start on the restored
data before the offline restore in step 8 runs. This step is mandatory: it stops those
pods so step 8 can rebuild the data directory with shared 3-member membership. A pod that
starts in the meantime is harmless — kube-apiserver is scaled to 0, so nothing reads or
writes etcd.

```bash
kubectl -n $HCP_NS scale statefulset/etcd --replicas=0
kubectl -n $HCP_NS wait --for=delete pod -l app=etcd --timeout=5m
```
---
## 7. Distribute ONE canonical snapshot to all three PVCs

Create a holder pod per PVC, compare snapshots, `kubectl cp` the freshest onto all three,
wipe stale data.

```bash
# 7a. holder pod per PVC
for i in 0 1 2; do
kubectl -n $HCP_NS apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata: { name: etcd-holder-${i}, labels: { app: etcd-holder } }
spec:
  restartPolicy: Never
  securityContext: { runAsUser: ${ETCD_UID}, fsGroup: ${ETCD_GID} }
  containers:
  - name: holder
    image: ${ETCD_IMG}
    command: ["/bin/sh","-c","sleep 3600"]
    volumeMounts: [{ name: data, mountPath: /var/lib }]
  volumes:
  - { name: data, persistentVolumeClaim: { claimName: data-etcd-${i} } }
EOF
done
kubectl -n $HCP_NS wait --for=condition=ready pod/etcd-holder-0 pod/etcd-holder-1 pod/etcd-holder-2 --timeout=3m

# 7b. compare each PVC's own snapshot — note the highest REVISION; that PVC is canonical
for i in 0 1 2; do
  echo "--- data-etcd-${i} ---"
  kubectl  -n $HCP_NS exec etcd-holder-${i} -- etcdutl snapshot status /var/lib/backup.db -w json
done

# 7c. pull canonical local (EDIT the source index to the freshest), verify, push to the other two
export CANON=<index>
kubectl -n $HCP_NS cp etcd-holder-${CANON}:/var/lib/backup.db /tmp/canonical-backup.db
shasum -a 256 /tmp/canonical-backup.db
for i in 0 1 2; do
  [ "$i" = "$CANON" ] && continue
  kubectl  -n $HCP_NS cp /tmp/canonical-backup.db etcd-holder-${i}:/var/lib/backup.db
done

# 7d. verify identical everywhere + wipe stale data dirs
for i in 0 1 2; do
  echo "--- holder-${i} ---"
  kubectl  -n $HCP_NS exec etcd-holder-${i} -- sh -ce '
    rm -rf /var/lib/data
    sha256sum /var/lib/backup.db
    etcdutl snapshot status /var/lib/backup.db -w json'
done

# 7e. GATE: the sha256 (and revision/hash) MUST be identical across all three before continuing
kubectl  -n $HCP_NS delete pod etcd-holder-0 etcd-holder-1 etcd-holder-2
```

---

## 8. Offline `etcdutl` restore per PVC (shared 3-member membership)

All three use the SAME `--initial-cluster` + token; differ only per-member in
`--name` / `--initial-advertise-peer-urls`; identical `--mark-compacted` /
`--bump-revision` so revisions line up.

> ### Why `--bump-revision 1000000000` (and `--mark-compacted`)
>
> A restored snapshot resumes at whatever revision it was taken at. That revision is
> almost always **lower** than the revision clients last saw before the outage — the
> kube-apiserver watch cache, informers, and anything tracking a `resourceVersion`
> all remember the pre-restore high-water mark. If etcd starts handing out revisions
> that go *backwards*, those clients see the store rewind: watches wait for a revision
> etcd won't reach again for a long time, so they never fire, and cached objects can
> silently disagree with what's actually in etcd.
>
> `--bump-revision` prevents this by adding a fixed offset to the snapshot's current
> revision, so the restored store starts *above* the last revision anyone observed. It
> takes a 64-bit integer: the number of revisions to add. Every write to etcd advances
> the revision by exactly one, so the offset is effectively "how many writes of
> headroom." `1000000000` (one billion) covers roughly a **week** of runtime for an
> etcd doing under ~1500 writes/sec (1e9 ÷ 1500 ≈ 7.7 days) — so a snapshot up to about
> a week stale still lands safely above the pre-outage revision.
>
> Implications of running it:
> - The bump is a **one-time jump**, not a leak. It does not grow the DB on disk, cost
>   storage, or degrade performance — the revision counter is 64-bit, so being ~1e9
>   higher is harmless.
> - All three members must restore with the **identical** bump value; that is what keeps
>   their revisions aligned so they boot as one consistent cluster.
> - Pick the offset larger than the outage/snapshot age. One billion is a safe default
>   for a snapshot no more than ~a week old; for an older snapshot, or a very
>   write-heavy cluster, increase it.
>
> `--mark-compacted` marks the bumped revision as the compaction point. In a Kubernetes
> context this is what actually **invalidates the stale watch caches**: watches
> established at the old (now-skipped) revisions are terminated with a "compacted" error,
> forcing informers to re-list against the new revision instead of hanging forever. For
> HCP/Kubernetes restores, always pair it with `--bump-revision`.
>
> Reference: [Restoring with revision bump](https://etcd.io/docs/v3.6/op-guide/recovery/#restoring-with-revision-bump)


```bash
export INITIAL_CLUSTER="etcd-0=https://etcd-0.etcd-discovery.$HCP_NS.svc:2380,etcd-1=https://etcd-1.etcd-discovery.$HCP_NS.svc:2380,etcd-2=https://etcd-2.etcd-discovery.$HCP_NS.svc:2380"

for i in 0 1 2; do
kubectl  -n $HCP_NS apply -f - <<EOF
apiVersion: batch/v1
kind: Job
metadata: { name: etcd-restore-${i}-$TS }
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      securityContext: { runAsUser: ${ETCD_UID}, fsGroup: ${ETCD_GID} }
      containers:
      - name: restore
        image: ${ETCD_IMG}
        command: ["/bin/sh","-ce"]
        args:
        - |
          etcdutl snapshot status /var/lib/backup.db -w table
          rm -rf /var/lib/data
          etcdutl snapshot restore /var/lib/backup.db \
            --data-dir=/var/lib/data \
            --name etcd-${i} \
            --initial-cluster-token etcd-cluster \
            --initial-cluster ${INITIAL_CLUSTER} \
            --initial-advertise-peer-urls https://etcd-${i}.etcd-discovery.$HCP_NS.svc:2380 \
            --mark-compacted \
            --bump-revision 1000000000
          rm -f /var/lib/backup.db
          ls -la /var/lib/data/member
        volumeMounts: [{ name: data, mountPath: /var/lib }]
      volumes:
      - { name: data, persistentVolumeClaim: { claimName: data-etcd-${i} } }
EOF
done

for i in 0 1 2; do
  kubectl  -n $HCP_NS wait --for=condition=complete job/etcd-restore-${i}-$TS --timeout=5m
  kubectl  -n $HCP_NS logs job/etcd-restore-${i}-$TS | grep -Ei "cluster-id|added member|restored snapshot|bumping"
done

# Validate restore was successful prior to removing pod
for i in 0 1 2; do
  kubectl  -n $HCP_NS delete job/etcd-restore-${i}-$TS
done
```

> **GATE:** all three logs must show the **same `cluster-id`** and the same three
> `added member` IDs. That is what makes them one cluster on boot.

Example from a verified restore — all three restore jobs baked the **same
`cluster-id`** and the same three member IDs, with the revision bumped by
`--bump-revision`:

```
cluster-id:      f5093b9a0719f101   (identical across etcd-restore-0/-1/-2)
added member:    610d33936f065854   name=etcd-0
added member:    af23a4bfd4b46012   name=etcd-1
added member:    8ac6a9abc18cf033   name=etcd-2
snapshot revision bumped to 1000011894
```

If any job shows a different `cluster-id` or a different member-ID set, do not
proceed — delete the restore jobs and re-run from step 7.

---

## 9. Start the cluster

```bash
kubectl  -n $HCP_NS scale statefulset/etcd --replicas=3
kubectl  -n $HCP_NS rollout status statefulset/etcd --timeout=10m
kubectl  -n $HCP_NS get pods -l app=etcd -o wide      # expect 3x 4/4 Running
```

---

## 10. Verify etcd is one healthy in-sync cluster

```bash
for p in etcd-0 etcd-1 etcd-2; do
  echo "== $p member list =="
  kubectl  -n $HCP_NS exec $p -c etcd -- sh -c '
    export ETCDCTL_CACERT=/etc/etcd/tls/etcd-ca/ca.crt
    export ETCDCTL_CERT=/etc/etcd/tls/client/etcd-client.crt
    export ETCDCTL_KEY=/etc/etcd/tls/client/etcd-client.key
    etcdctl --endpoints=https://localhost:2379 member list -w table'
done

kubectl  -n $HCP_NS exec etcd-0 -c etcd -- sh -c "
  export ETCDCTL_CACERT=/etc/etcd/tls/etcd-ca/ca.crt
  export ETCDCTL_CERT=/etc/etcd/tls/client/etcd-client.crt
  export ETCDCTL_KEY=/etc/etcd/tls/client/etcd-client.key
  EP=https://etcd-0.etcd-discovery.$HCP_NS.svc:2379,https://etcd-1.etcd-discovery.$HCP_NS.svc:2379,https://etcd-2.etcd-discovery.$HCP_NS.svc:2379
  etcdctl --endpoints=\$EP endpoint status -w table
  etcdctl --endpoints=\$EP endpoint health"
```

**Success criteria:**
- Every node's `member list` shows the **same 3 `started` members**, each with BOTH
  peer and client addrs and `IS LEARNER=false` — **no phantom / empty-client-addr** entry.
- `endpoint status`: exactly **one `IS LEADER=true`**, same `RAFT TERM`, matching
  `RAFT INDEX` across members, all with real `DB SIZE`.
- `endpoint health`: all three healthy.

Example output from a verified restore. Each node's `member list` is identical (only
`etcd-0`'s is shown here); the same three `started` members appear on all three, both
addrs populated, `IS LEARNER=false`, no phantom entry:

```
================ etcd-0 : member list ================
+------------------+---------+--------+---------------------------+---------------------------+------------+
|        ID        | STATUS  |  NAME  |         PEER ADDRS        |        CLIENT ADDRS       | IS LEARNER |
+------------------+---------+--------+---------------------------+---------------------------+------------+
| 610d33936f065854 | started | etcd-0 | https://etcd-0…svc:2380   | https://etcd-0…svc:2379   |      false |
| 8ac6a9abc18cf033 | started | etcd-2 | https://etcd-2…svc:2380   | https://etcd-2…svc:2379   |      false |
| af23a4bfd4b46012 | started | etcd-1 | https://etcd-1…svc:2380   | https://etcd-1…svc:2379   |      false |
+------------------+---------+--------+---------------------------+---------------------------+------------+
```

`endpoint status` — exactly one `IS LEADER=true` (etcd-1), matching `RAFT TERM`/`RAFT INDEX`
and real `DB SIZE` on all three:

```
+-------------------------+------------------+---------+---------+--------+-----------+------------+------------+
|         ENDPOINT        |        ID        | VERSION | DB SIZE | IN USE | IS LEADER | RAFT TERM  | RAFT INDEX |
+-------------------------+------------------+---------+---------+--------+-----------+------------+------------+
| https://etcd-0…svc:2379 | 610d33936f065854 |  3.6.13 |   43 MB |  24 MB |     false |         2  |          9 |
| https://etcd-1…svc:2379 | af23a4bfd4b46012 |  3.6.13 |   43 MB |  24 MB |      true |         2  |          9 |
| https://etcd-2…svc:2379 | 8ac6a9abc18cf033 |  3.6.13 |   43 MB |  26 MB |     false |         2  |          9 |
+-------------------------+------------------+---------+---------+--------+-----------+------------+------------+
```

`endpoint health` — all three healthy:

```
https://etcd-1…svc:2379 is healthy: successfully committed proposal: took = 42.204786ms
https://etcd-0…svc:2379 is healthy: successfully committed proposal: took = 43.887896ms
https://etcd-2…svc:2379 is healthy: successfully committed proposal: took = 45.066005ms
```

---
## 11. Unpause the HostedCluster

```bash
kubectl  -n $HC_NS patch hostedcluster/$HC --type=merge \
  -p '{"spec":{"pausedUntil":null}}'
```

```bash
kubectl  -n $HCP_NS get hostedcontrolplane $HC \
  -o jsonpath='{range .status.conditions[?(@.type=="EtcdAvailable")]}{.type}={.status} ({.reason}){"\n"}{end}'
# expect: EtcdAvailable=True (QuorumAvailable)
```

CPO reconciles and brings KAS (and dependents) back. If KAS stays at `replicas=0`,
check `hostedcontrolplane` conditions — a blocked reconcile
(`ValidHostedControlPlaneConfiguration=False`, `ValidAzureKMSConfig=False`,
`ResourceGroupNotFound`) means an Azure infra/KMS problem **outside etcd**, not this
procedure.

---
## 12. Re-enable backup schedules

Reverse [step 1](#1-pause-backup-schedules-for-the-cluster) — with etcd restored and the
HostedCluster reconciling, resume scheduled backups.

### Dev environments (direct HTTP)

```bash
curl -s -X PATCH "$BACKUP_SCHEDULES" \
  -H 'X-Ms-Client-Principal-Name: localtest@redhat.com' \
  -H 'X-Ms-Client-Principal-Type: dstsUser' \
  -H 'Content-Type: application/json' \
  -d '{"state": "Enabled"}'
```

Confirm each schedule reports `"backupExecutionState": "Active"` via the admin API:
```bash
curl -s "$BACKUP_SCHEDULES" \
  -H 'X-Ms-Client-Principal-Name: localtest@redhat.com' \
  -H 'X-Ms-Client-Principal-Type: dstsUser' | jq
```

...or, with management-cluster access, confirm the `PAUSED` column is blank (empty) for
every schedule:
```bash
kubectl get schedules -n velero | grep "${HC_NS}"
```
```
NAME                                                     STATUS    SCHEDULE      LASTBACKUP   AGE   PAUSED
ocm-arohcppers-2sd1pej7kdkk1qvccoiqq534ae1knhh4-daily    Enabled   0 2 * * *                  36m
ocm-arohcppers-2sd1pej7kdkk1qvccoiqq534ae1knhh4-hourly   Enabled   0 */1 * * *                36m
ocm-arohcppers-2sd1pej7kdkk1qvccoiqq534ae1knhh4-weekly   Enabled   0 3 * * 0                  36m
```

### MSFT environments (Geneva Action)

Use the Geneva Action:

> **Azure Red Hat Hypershift > Fleet Operations > Update Backup Schedule**

Enter the **HCP Resource ID** and set **Schedule State** to `Enabled`.

Verify with the companion lookup Geneva Action, supplying the same **HCP Resource ID**:

> **Azure Red Hat Hypershift > Fleet Operations > Get Backup Schedule**

Every schedule should report `"backupExecutionState": "Active"`.
