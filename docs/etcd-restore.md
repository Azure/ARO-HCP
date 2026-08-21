# SOP — Restore a HyperShift (ARO-HCP) etcd cluster to 3 members from a Velero backup

**What this does:** rebuilds a Managed 3-member etcd cluster for a HostedControlPlane
from a Velero datamover backup, using one canonical snapshot restored identically
into all three PVCs (so all three boot as ONE `state=new` cluster). This is the
"Method C" path, proven end-to-end on 2026-08-20.

**When to use:** etcd data loss / split-brain / a member restored single-node, and
you need a clean HA etcd from a known-good backup. HA (3-member) HCPs only.

---

## CAUTION

1. **Do NOT grow the StatefulSet 1→2→3.** The stock STS hardcodes a 3-member
   `ETCD_INITIAL_CLUSTER`; etcd 3.6 rejects an `existing` join whenever real
   membership ≠ 3 (`member count is unequal`), and `reset-member` re-adds as a
   voter, killing quorum. Restore all three together instead.
2. **The three per-pod `backup.db` files are usually DIFFERENT snapshots** (the
   Velero pre-hook fires per-pod at slightly different instants). Copy the freshest
   (highest revision) to all three — the copy is mandatory.
3. This process restore fixes **etcd only**.

---

## 0. Parameters — set once per shell

```bash
export KC=/path/to/mgmt.kubeconfig
export HC_NS=<hostedcluster namespace>     # where the HostedCluster lives, e.g. ocm-...
export HC=<hostedcluster name>             # e.g. f3e7i2o6r4k9b0q
export HCP_NS=<control-plane namespace>    # etcd namespace = ${HC_NS}-${HC}
export BACKUP=<velero backup name>         # the datamover backup to restore from
export TS=$(date +%Y%m%d%H%M%S)
```

**Discover the rest from the live cluster (don't hardcode):**

```bash
# etcd image (has etcdutl/etcdctl + tar)
# pod identity used by the etcd pod (match it so restored files are owned correctly)

export ETCD_IMG=$(kubectl --kubeconfig=$KC -n $HCP_NS get statefulset etcd \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="etcd")].image}')
export ETCD_UID=$(kubectl --kubeconfig=$KC -n $HCP_NS get statefulset etcd -o jsonpath='{.spec.template.spec.securityContext.runAsUser}')
export ETCD_GID=$(kubectl --kubeconfig=$KC -n $HCP_NS get statefulset etcd -o jsonpath='{.spec.template.spec.securityContext.fsGroup}')
echo "ETCD_IMG=$ETCD_IMG  ETCD_UID=$ETCD_UID  ETCD_GID=$ETCD_GID"
```
---

## 1. Pause the HostedCluster (stop CPO from re-scaling etcd / recreating PVCs)

```bash
kubectl --kubeconfig=$KC -n $HC_NS patch hostedcluster/$HC --type=merge \
  -p '{"spec":{"pausedUntil":"true"}}'
kubectl --kubeconfig=$KC -n $HC_NS get hostedcluster/$HC -o jsonpath='pausedUntil={.spec.pausedUntil}{"\n"}'
```

## 2. Scale etcd and KAS to 0

```bash
kubectl --kubeconfig=$KC -n $HCP_NS scale statefulset/etcd --replicas=0
kubectl --kubeconfig=$KC -n $HCP_NS scale deployment/kube-apiserver --replicas=0
kubectl --kubeconfig=$KC -n $HCP_NS wait --for=delete pod -l app=etcd --timeout=5m
```

## 3. Delete the three etcd PVCs

Velero restore needs the target PVC absent. PV reclaim policy is `Delete`, so the
Azure disks go too — fine, you're re-provisioning from CSI Snapshot

```bash
kubectl --kubeconfig=$KC -n $HCP_NS delete pvc data-etcd-0 data-etcd-1 data-etcd-2 --wait=true
```

## 4. Restore all three etcd PVCs from the datamover backup

PVCs are labeled `app=etcd` only (no per-member label), so restore all three.

StatefulSet is restored since the PVCs need something to bind to for the underlying PV to be provisioned and restored
from the CSI Snapshot.
```bash
export RESTORE_NAME=restore-etcd-$TS

cat <<EOF | kubectl --kubeconfig=$KC apply -f -
apiVersion: velero.io/v1
kind: Restore
metadata:
  name: $RESTORE_NAME
  namespace: velero
spec:
  backupName: $BACKUP
  restorePVs: true
  existingResourcePolicy: update
  includedResources:
  - pv
  - pvc
  - statefulsets
  itemOperationTimeout: 4h0m0s
EOF

watch -n 10 "kubectl --kubeconfig=$KC get restore $RESTORE_NAME -n velero -ojsonpath='{.status}' | jq"
kubectl --kubeconfig=$KC -n $HCP_NS get pvc -l app=etcd     # wait until all three Bound
```

---
## 5. C-1 — Distribute ONE canonical snapshot to all three PVCs

Create a holder pod per PVC, compare snapshots, `kubectl cp` the freshest onto all three,
wipe stale data.

```bash
# 5a. holder pod per PVC
for i in 0 1 2; do
kubectl --kubeconfig=$KC -n $HCP_NS apply -f - <<EOF
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
kubectl --kubeconfig=$KC -n $HCP_NS wait --for=condition=ready pod/etcd-holder-0 pod/etcd-holder-1 pod/etcd-holder-2 --timeout=3m

# 5b. compare each PVC's own snapshot — note the highest REVISION; that PVC is canonical
for i in 0 1 2; do
  echo "--- data-etcd-${i} ---"
  kubectl --kubeconfig=$KC -n $HCP_NS exec etcd-holder-${i} -- etcdutl snapshot status /var/lib/backup.db -w json
done

# 5c. pull canonical local (EDIT the source index to the freshest), verify, push to the other two
export CANON=<index>
kubectl --kubeconfig=$KC -n $HCP_NS cp etcd-holder-${CANON}:/var/lib/backup.db /Users/tschneid/devel/tmp/canonical-backup.db
shasum -a 256 /Users/tschneid/devel/tmp/canonical-backup.db
for i in 0 1 2; do
  [ "$i" = "$CANON" ] && continue
  kubectl --kubeconfig=$KC -n $HCP_NS cp /Users/tschneid/devel/tmp/canonical-backup.db etcd-holder-${i}:/var/lib/backup.db
done

# 5d. verify identical everywhere + wipe stale data dirs and any partial file
for i in 0 1 2; do
  echo "--- holder-${i} ---"
  kubectl --kubeconfig=$KC -n $HCP_NS exec etcd-holder-${i} -- sh -ce '
    rm -f /var/lib/backup.db.part
    rm -rf /var/lib/data
    sha256sum /var/lib/backup.db
    etcdutl snapshot status /var/lib/backup.db -w json'
done

# 5e. GATE: the sha256 (and revision/hash) MUST be identical across all three before continuing
kubectl --kubeconfig=$KC -n $HCP_NS delete pod etcd-holder-0 etcd-holder-1 etcd-holder-2
```

---

## 6. C-2 — Offline `etcdutl` restore per PVC (shared 3-member membership)

All three use the SAME `--initial-cluster` + token; differ only per-member in
`--name` / `--initial-advertise-peer-urls`; identical `--mark-compacted` /
`--bump-revision` so revisions line up. `--bump-revision 1000000000` keeps the
restored store above any surviving watch-cache revision; `--mark-compacted` pins
compaction. Drop both only for a pure "does data come back" test.

```bash
export INITIAL_CLUSTER="etcd-0=https://etcd-0.etcd-discovery.$HCP_NS.svc:2380,etcd-1=https://etcd-1.etcd-discovery.$HCP_NS.svc:2380,etcd-2=https://etcd-2.etcd-discovery.$HCP_NS.svc:2380"

for i in 0 1 2; do
kubectl --kubeconfig=$KC -n $HCP_NS apply -f - <<EOF
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
  kubectl --kubeconfig=$KC -n $HCP_NS wait --for=condition=complete job/etcd-restore-${i}-$TS --timeout=5m
  kubectl --kubeconfig=$KC -n $HCP_NS logs job/etcd-restore-${i}-$TS | grep -Ei "cluster-id|added member|restored snapshot|bumping"
  kubectl --kubeconfig=$KC -n $HCP_NS delete job/etcd-restore-${i}-$TS
done
```

> **GATE:** all three logs must show the **same `cluster-id`** and the same three
> `added member` IDs. That is what makes them one cluster on boot.

---

## 7. C-3 — Start the cluster

```bash
kubectl --kubeconfig=$KC -n $HCP_NS scale statefulset/etcd --replicas=3
kubectl --kubeconfig=$KC -n $HCP_NS rollout status statefulset/etcd --timeout=10m
kubectl --kubeconfig=$KC -n $HCP_NS get pods -l app=etcd -o wide      # expect 3x 4/4 Running
```

---

## 8. Verify etcd is one healthy in-sync cluster

```bash
for p in etcd-0 etcd-1 etcd-2; do
  echo "== $p member list =="
  kubectl --kubeconfig=$KC -n $HCP_NS exec $p -c etcd -- sh -c '
    export ETCDCTL_CACERT=/etc/etcd/tls/etcd-ca/ca.crt
    export ETCDCTL_CERT=/etc/etcd/tls/client/etcd-client.crt
    export ETCDCTL_KEY=/etc/etcd/tls/client/etcd-client.key
    etcdctl --endpoints=https://localhost:2379 member list -w table'
done

kubectl --kubeconfig=$KC -n $HCP_NS exec etcd-0 -c etcd -- sh -c "
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

---
## 9. Unpause the HostedCluster

```bash
kubectl --kubeconfig=$KC -n $HC_NS patch hostedcluster/$HC --type=merge \
  -p '{"spec":{"pausedUntil":null}}'
```

```bash
kubectl --kubeconfig=$KC -n $HCP_NS get hostedcontrolplane $HC \
  -o jsonpath='{range .status.conditions[?(@.type=="EtcdAvailable")]}{.type}={.status} ({.reason}){"\n"}{end}'
# expect: EtcdAvailable=True (QuorumAvailable)
```

CPO reconciles and brings KAS (and dependents) back. If KAS stays at `replicas=0`,
check `hostedcontrolplane` conditions — a blocked reconcile
(`ValidHostedControlPlaneConfiguration=False`, `ValidAzureKMSConfig=False`,
`ResourceGroupNotFound`) means an Azure infra/KMS problem **outside etcd**, not this
procedure.

---

## Rollback / safety notes

- All work happens while the HC is **paused** and KAS is **scaled to 0**; nothing
  serves traffic mid-procedure.
- The GATES in steps 5e and 6 are the guardrails against the split-brain failure
  mode (mismatched snapshots or mismatched cluster-ids). Do not skip them.
- If a GATE fails, delete the holder/restore pods and re-run from step 5 — the PVC
  data dirs are recreated by the restore jobs, so retries are safe.
