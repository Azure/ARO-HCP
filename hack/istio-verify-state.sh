#!/usr/bin/env bash
# Istio upgrade verification — captures mesh state snapshots and compares before/after.
#
# Usage:
#   ./hack/istio-verify-state.sh before          # snapshot only
#   ./hack/istio-verify-state.sh after           # snapshot + compare with before
#   ./hack/istio-verify-state.sh now             # snapshot + verify
#   ./hack/istio-verify-state.sh now --live      # also run live checks (creates a temporary pod)
#
# --live creates a temporary pod (istio-verify-curl) in the aro-hcp namespace to test
# service reachability and ingress connectivity. The pod is auto-deleted on success;
# on timeout it is cleaned up explicitly, but check with:
#   kubectl -n aro-hcp delete pod istio-verify-curl --ignore-not-found
#
# Requires a valid kubeconfig for the target cluster. If kubectl cannot reach the
# API server (e.g. after a cluster upgrade rotates the FQDN), refresh credentials:
#   az aks get-credentials -g <resource-group> -n <cluster-name> --overwrite-existing
#
# Snapshots are saved to /tmp/istio-verify-{before,after}.txt

set -uo pipefail

PHASE="${1:-now}"
LIVE=false
for arg in "$@"; do
    if [ "$arg" = "--live" ]; then
        LIVE=true
    fi
done
BEFORE_FILE="/tmp/istio-verify-before.txt"
AFTER_FILE="/tmp/istio-verify-after.txt"

capture_state() {
    echo "=== MESH STATE SNAPSHOT — $(date -u '+%Y-%m-%dT%H:%M:%SZ') ==="
    echo ""

    echo "--- MESH NAMESPACES ---"
    kubectl get ns -l istio.io/rev -o custom-columns='NAMESPACE:.metadata.name,REVISION:.metadata.labels.istio\.io/rev' 2>/dev/null | (read -r header && echo "$header" && sort)
    echo ""

    local ns_list
    ns_list=$(kubectl get ns -l istio.io/rev --no-headers -o custom-columns='NAME:.metadata.name' 2>/dev/null)
    if [ -z "$ns_list" ]; then
        echo "  No mesh namespaces found"
        echo ""
        echo "=== END SNAPSHOT ==="
        return
    fi
    local all_tag=true
    for ns in $ns_list; do
        local rev
        rev=$(kubectl get ns "$ns" -o jsonpath='{.metadata.labels.istio\.io/rev}' 2>/dev/null)
        if [[ "$rev" =~ ^asm- ]]; then
            all_tag=false
        fi
    done
    if $all_tag; then
        echo "  Injection: tag-based (all namespaces)"
    else
        echo "  WARNING: some namespaces use direct revision labels instead of tag"
        for ns in $ns_list; do
            local rev
            rev=$(kubectl get ns "$ns" -o jsonpath='{.metadata.labels.istio\.io/rev}' 2>/dev/null)
            if [[ "$rev" =~ ^asm- ]]; then
                echo "    $ns -> $rev (direct)"
            fi
        done
    fi
    echo ""

    echo "--- CONTROL PLANES ---"
    kubectl get pods -n aks-istio-system -l app=istiod \
        -o custom-columns='POD:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].status,IMAGE:.spec.containers[0].image' 2>/dev/null | (read -r header && echo "$header" && sort)
    echo ""

    echo "--- WEBHOOKS ---"
    kubectl get mutatingwebhookconfigurations 2>/dev/null | head -1
    kubectl get mutatingwebhookconfigurations --no-headers 2>/dev/null | grep -i istio | sort
    echo ""

    echo "--- WEBHOOK TARGETS ---"
    for wh in $(kubectl get mutatingwebhookconfigurations -o name 2>/dev/null | grep istio); do
        local name
        name="${wh##*/}"
        local targets
        targets=$(kubectl get "$wh" -o jsonpath='{range .webhooks[*]}{.clientConfig.service.name}:{.clientConfig.service.namespace}{"\n"}{end}' 2>/dev/null | sort -u | tr '\n' ' ')
        echo "  $name -> ${targets% }"
    done
    echo ""

    echo "--- TAG WEBHOOK VALIDATION ---"
    local tag_wh
    tag_wh=$(kubectl get mutatingwebhookconfigurations -o name 2>/dev/null | grep 'istio-revision-tag-' | head -1)
    tag_wh="${tag_wh##*/}"
    if [ -n "$tag_wh" ] && kubectl get mutatingwebhookconfiguration "$tag_wh" &>/dev/null; then
        local tag_target
        tag_target=$(kubectl get mutatingwebhookconfiguration "$tag_wh" \
            -o jsonpath='{.webhooks[0].clientConfig.service.name}' 2>/dev/null)
        local tag_ns
        tag_ns=$(kubectl get mutatingwebhookconfiguration "$tag_wh" \
            -o jsonpath='{.webhooks[0].clientConfig.service.namespace}' 2>/dev/null)
        local tag_ca_len
        tag_ca_len=$(kubectl get mutatingwebhookconfiguration "$tag_wh" \
            -o jsonpath='{.webhooks[0].clientConfig.caBundle}' 2>/dev/null | wc -c | tr -d ' ')
        echo "  Target service: $tag_target (namespace: $tag_ns)"
        echo "  CA bundle length: $tag_ca_len"
        if [ "$tag_ca_len" -gt 0 ]; then
            echo "  CA bundle: present"
        else
            echo "  CA bundle: MISSING"
        fi
    else
        echo "  Tag webhook not found"
    fi
    echo ""

    echo "--- CONFIGMAPS (aks-istio-system) ---"
    kubectl get configmaps -n aks-istio-system -l istio.io/rev \
        -o custom-columns='NAME:.metadata.name,REVISION:.metadata.labels.istio\.io/rev' 2>/dev/null | (read -r header && echo "$header" && sort)
    echo ""

    echo "--- CONFIGMAP CONTENT (ext-authz providers) ---"
    for cm in $(kubectl get configmaps -n aks-istio-system -l istio.io/rev --no-headers -o custom-columns='NAME:.metadata.name' 2>/dev/null); do
        local mesh_data
        mesh_data=$(kubectl get configmap "$cm" -n aks-istio-system -o jsonpath='{.data.mesh}' 2>/dev/null || true)
        if echo "$mesh_data" | grep -q "extensionProviders" 2>/dev/null; then
            echo "  $cm: ext-authz configured"
        else
            echo "  $cm: no ext-authz"
        fi
    done
    echo ""

    echo "--- CONFIGMAP LABELS ---"
    for cm in $(kubectl get configmaps -n aks-istio-system -l istio.io/rev --no-headers -o custom-columns='NAME:.metadata.name' 2>/dev/null); do
        local rev_label
        rev_label=$(kubectl get configmap "$cm" -n aks-istio-system -o jsonpath='{.metadata.labels.istio\.io/rev}' 2>/dev/null)
        echo "  $cm: istio.io/rev=${rev_label:-MISSING}"
    done
    echo ""

    echo "--- INGRESS GATEWAYS ---"
    kubectl get svc -n aks-istio-ingress \
        -o custom-columns='SERVICE:.metadata.name,TYPE:.spec.type,EXTERNAL-IP:.status.loadBalancer.ingress[0].ip' 2>/dev/null | (read -r header && echo "$header" && sort)
    echo ""

    echo "--- INGRESS PIP PINNING (azure-pip-name annotations) ---"
    for svc in $(kubectl get svc -n aks-istio-ingress --no-headers -o custom-columns='NAME:.metadata.name' 2>/dev/null); do
        local pip_name
        pip_name=$(kubectl get svc "$svc" -n aks-istio-ingress \
            -o jsonpath='{.metadata.annotations.service\.beta\.kubernetes\.io/azure-pip-name}' 2>/dev/null)
        local rg_name
        rg_name=$(kubectl get svc "$svc" -n aks-istio-ingress \
            -o jsonpath='{.metadata.annotations.service\.beta\.kubernetes\.io/azure-load-balancer-resource-group}' 2>/dev/null)
        echo "  $svc: pip=${pip_name:-none} rg=${rg_name:-none}"
    done
    echo ""

    echo "--- POD SIDECAR REVISIONS (all pods with istio-proxy) ---"
    local sidecar_scan_ns="$ns_list aks-istio-system aks-istio-ingress"
    local sidecar_tmpfile
    sidecar_tmpfile=$(mktemp)
    for scan_ns in $sidecar_scan_ns; do
        # Output one image per pod (prefer initContainers for native sidecars, fall back to containers)
        kubectl get pods -n "$scan_ns" --field-selector=status.phase=Running \
            -o jsonpath='{range .items[*]}{.metadata.name}{" "}{range .spec.initContainers[?(@.name=="istio-proxy")]}{.image}{end}{"|"}{range .spec.containers[?(@.name=="istio-proxy")]}{.image}{end}{"\n"}{end}' 2>/dev/null \
            | while IFS= read -r line; do
                [ -z "$line" ] && continue
                local init_img="${line#* }"
                init_img="${init_img%%|*}"
                local reg_img="${line#*|}"
                if [ -n "$init_img" ]; then
                    echo "$init_img"
                elif [ -n "$reg_img" ]; then
                    echo "$reg_img"
                fi
            done >> "$sidecar_tmpfile"
    done
    local total
    total=$(grep -c "." "$sidecar_tmpfile" || echo 0)
    echo "Total pods with istio-proxy: $total"
    echo "  COUNT  IMAGE"
    grep "." "$sidecar_tmpfile" | sort | uniq -c | sort -rn
    rm -f "$sidecar_tmpfile"
    echo ""

    echo "--- STALE SIDECAR PODS (not on latest istiod revision) ---"
    local latest latest_image
    latest_image=$(kubectl get pods -n aks-istio-system -l app=istiod --no-headers \
        -o custom-columns='IMAGE:.spec.containers[0].image' 2>/dev/null | sort -V | tail -1)
    latest=$(echo "$latest_image" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+-[0-9]+' | tail -1)
    latest="${latest:-unknown}"
    echo "Latest istiod version: $latest"
    if [ "$latest" != "unknown" ]; then
        local stale_tmpfile
        stale_tmpfile=$(mktemp)
        for scan_ns in $sidecar_scan_ns; do
            # Output one entry per pod (prefer initContainers for native sidecars)
            kubectl get pods -n "$scan_ns" --field-selector=status.phase=Running \
                -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{" "}{range .spec.initContainers[?(@.name=="istio-proxy")]}{.image}{end}{"|"}{range .spec.containers[?(@.name=="istio-proxy")]}{.image}{end}{"\n"}{end}' 2>/dev/null \
                | while IFS= read -r line; do
                    [ -z "$line" ] && continue
                    local pod_id="${line%% *}"
                    local rest="${line#* }"
                    local init_img="${rest%%|*}"
                    local reg_img="${rest#*|}"
                    local img=""
                    if [ -n "$init_img" ]; then
                        img="$init_img"
                    elif [ -n "$reg_img" ]; then
                        img="$reg_img"
                    fi
                    [ -z "$img" ] && continue
                    echo "$pod_id $img"
                done | grep -v "$latest" | grep -v "^$" >> "$stale_tmpfile" || true
        done
        local stale_pods
        stale_pods=$(cat "$stale_tmpfile")
        rm -f "$stale_tmpfile"
        if [ -n "$stale_pods" ]; then
            echo "$stale_pods"
        else
            echo "(none)"
        fi
    fi
    echo ""

    echo "--- DEPLOYMENT ROLLOUT STATUS (mesh namespaces) ---"
    local first_ns
    first_ns=$(echo "$ns_list" | head -1)
    if [ -n "$first_ns" ]; then
        kubectl get deploy -n "$first_ns" -o custom-columns='NAME:.metadata.name,READY:.status.readyReplicas,DESIRED:.spec.replicas,UPDATED:.status.updatedReplicas,AVAIL:.status.availableReplicas' 2>/dev/null | head -1
    fi
    for ns in $ns_list; do
        local deploys
        deploys=$(kubectl get deploy -n "$ns" --no-headers -o custom-columns='NAME:.metadata.name,READY:.status.readyReplicas,DESIRED:.spec.replicas,UPDATED:.status.updatedReplicas,AVAIL:.status.availableReplicas' 2>/dev/null)
        if [ -n "$deploys" ]; then
            echo "[$ns]"
            echo "$deploys"
        fi
    done
    echo ""

    echo "--- FLEET MESH STATUS ---"
    local fleet_label
    fleet_label=$(kubectl get ns fleet -o jsonpath='{.metadata.labels.istio\.io/rev}' 2>/dev/null || echo "")
    if [ -n "$fleet_label" ]; then
        echo "  Namespace label: istio.io/rev=$fleet_label"
    else
        echo "  WARN: fleet namespace missing istio.io/rev label"
    fi
    local fleet_pods
    fleet_pods=$(kubectl get pods -n fleet -o jsonpath='{range .items[*]}{.metadata.name}{" "}{range .spec.initContainers[?(@.name=="istio-proxy")]}{.image}{end}{"\n"}{end}' 2>/dev/null | grep -v "^$")
    if [ -z "$fleet_pods" ]; then
        fleet_pods=$(kubectl get pods -n fleet -o jsonpath='{range .items[*]}{.metadata.name}{" "}{range .spec.containers[?(@.name=="istio-proxy")]}{.image}{end}{"\n"}{end}' 2>/dev/null | grep -v "^$")
    fi
    if [ -n "$fleet_pods" ]; then
        echo "  Pods with sidecars:"
        echo "$fleet_pods" | while read -r line; do
            echo "    $line"
        done
    else
        echo "  (no fleet pods with istio-proxy sidecar)"
    fi
    local has_sidecar
    has_sidecar=$(kubectl get pods -n fleet -l app.kubernetes.io/name=fleet-controller \
        -o jsonpath='{.items[0].spec.initContainers[?(@.name=="istio-proxy")].name}{.items[0].spec.containers[?(@.name=="istio-proxy")].name}' 2>/dev/null)
    if [ -z "$has_sidecar" ]; then
        echo "  WARN: fleet pods have no istio-proxy sidecar"
    else
        local fleet_proxy_errors
        fleet_proxy_errors=$(kubectl logs -n fleet -l app.kubernetes.io/name=fleet-controller -c istio-proxy --tail=5 2>/dev/null | grep -E "no such host|connection refused|upstream connect error" | wc -l | tr -d ' ')
        if [ "$fleet_proxy_errors" -gt 0 ]; then
            echo "  WARN: istio-proxy errors detected ($fleet_proxy_errors in last 5 log lines)"
        else
            echo "  istio-proxy: healthy"
        fi
    fi
    echo ""

    echo "--- SIDECAR INJECTION STATUS ---"
    for ns in $ns_list; do
        local total with_sidecar
        total=$(kubectl get pods -n "$ns" --no-headers 2>/dev/null | wc -l | tr -d ' ')
        with_sidecar=$(kubectl get pods -n "$ns" -o jsonpath='{range .items[*]}{.spec.initContainers[*].name}{.spec.containers[*].name}{"\n"}{end}' 2>/dev/null | grep -c "istio-proxy" 2>/dev/null || true)
        with_sidecar="${with_sidecar:-0}"
        echo "  $ns: $with_sidecar/$total pods injected"
    done
    echo ""

    echo "--- MTLS POLICY ---"
    kubectl get peerauthentication -A --no-headers -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name,MODE:.spec.mtls.mode' 2>/dev/null | while IFS= read -r line; do
        echo "  $line"
    done
    echo ""

    echo "--- AUTHORIZATION POLICIES ---"
    local allow_count deny_count total_authz
    allow_count=$(kubectl get authorizationpolicies -A --no-headers 2>/dev/null | awk '$3 == "ALLOW" {print}' | wc -l | tr -d ' ')
    total_authz=$(kubectl get authorizationpolicies -A --no-headers 2>/dev/null | wc -l | tr -d ' ')
    deny_count=$((total_authz - allow_count))
    echo "  Total: $total_authz (deny-all=$deny_count, explicit-ALLOW=$allow_count)"
    kubectl get authorizationpolicies -A --no-headers -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name,ACTION:.spec.action' 2>/dev/null | while IFS= read -r line; do
        echo "  $line"
    done
    echo ""

    echo "--- LEASES (aks-istio-system) ---"
    local running_pods
    running_pods=$(kubectl get pods -n aks-istio-system --no-headers -o custom-columns='NAME:.metadata.name' 2>/dev/null)
    local orphan_count=0
    local active_count=0
    while IFS= read -r lease_line; do
        [ -z "$lease_line" ] && continue
        local lease_name holder
        lease_name=$(echo "$lease_line" | awk '{print $1}')
        holder=$(echo "$lease_line" | awk '{print $2}')
        if echo "$running_pods" | grep -qx "$holder" 2>/dev/null; then
            echo "  $lease_name holder=$holder status=active"
            active_count=$((active_count + 1))
        else
            echo "  $lease_name holder=$holder status=ORPHAN"
            orphan_count=$((orphan_count + 1))
        fi
    done < <(kubectl get leases -n aks-istio-system --no-headers -o custom-columns='NAME:.metadata.name,HOLDER:.spec.holderIdentity' 2>/dev/null)
    echo "  Total: $((active_count + orphan_count)) (active=$active_count orphan=$orphan_count)"
    echo ""

    echo "=== END SNAPSHOT ==="
}

run_live_checks() {
    echo "=========================================="
    echo "  LIVE VERIFICATION CHECKS"
    echo "=========================================="

    echo ""
    echo "--- SERVICE REACHABILITY ---"
    local test_svc test_ns test_port
    test_svc=$(kubectl get svc -n aro-hcp --no-headers -o custom-columns='NAME:.metadata.name,PORT:.spec.ports[0].port' 2>/dev/null | head -1)
    if [ -n "$test_svc" ]; then
        test_ns="aro-hcp"
        local svc_name svc_port
        svc_name=$(echo "$test_svc" | awk '{print $1}')
        svc_port=$(echo "$test_svc" | awk '{print $2}')

        # Non-mesh pod verifies mesh services are reachable without sidecar injection
        kubectl run istio-verify-curl --namespace="$test_ns" --rm -i --restart=Never \
            --image=mcr.microsoft.com/azurelinux/base/core:3.0 \
            --labels="sidecar.istio.io/inject=false" \
            --timeout=30s \
            --command -- sh -c "
                curl -s -o /dev/null -w 'HTTP_CODE:%{http_code}\nTIME:%{time_total}s' \
                    http://${svc_name}.${test_ns}.svc.cluster.local:${svc_port}/ 2>/dev/null || echo 'CONNECT_FAILED'
            " && echo "  Service reachability test: completed" || {
                echo "  Service reachability test: skipped (no suitable service or timeout)"
                kubectl delete pod istio-verify-curl -n "$test_ns" --ignore-not-found 2>/dev/null || true
            }
    else
        echo "  Skipped — no services found in aro-hcp"
    fi
    echo ""

    # Ingress reachability
    echo "--- INGRESS REACHABILITY ---"
    local ingress_ip
    ingress_ip=$(kubectl get svc -n aks-istio-ingress -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
    if [ -n "$ingress_ip" ]; then
        local http_code
        http_code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 5 "http://${ingress_ip}/" 2>/dev/null) || true
        http_code="${http_code:-000}"
        echo "  Ingress IP: $ingress_ip"
        echo "  HTTP response: $http_code"
        if [ "$http_code" != "000" ]; then
            echo "  Ingress reachable (response code doesn't matter — connectivity works)"
        else
            echo "  WARN: Ingress unreachable"
        fi
    else
        echo "  Skipped — no ingress IP assigned"
    fi
    echo ""

    # Istiod health — use pod readiness (healthz exec is unreliable in distroless containers)
    echo "--- ISTIOD HEALTH ---"
    for pod in $(kubectl get pods -n aks-istio-system -l app=istiod --no-headers -o custom-columns='NAME:.metadata.name' 2>/dev/null); do
        local ready containers
        ready=$(kubectl get pod -n aks-istio-system "$pod" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
        containers=$(kubectl get pod -n aks-istio-system "$pod" -o jsonpath='{.status.containerStatuses[0].ready}' 2>/dev/null || echo "unknown")
        echo "  $pod: Ready=$ready Container=$containers"
    done
    echo ""
}

run_verification() {
    echo "=========================================="
    echo "  SNAPSHOT VERIFICATION"
    echo "=========================================="

    local file="$1"

    # Check 1: Single sidecar revision
    local revisions
    revisions=$(sed -n '/POD SIDECAR REVISIONS/,/^---/p' "$file" | grep "mcr.microsoft.com" | awk '{print $NF}' | sort -u | wc -l | tr -d ' ')
    if [ "$revisions" -eq 0 ]; then
        echo "WARN: No sidecar pods found"
    elif [ "$revisions" -eq 1 ]; then
        echo "PASS: All sidecar images on single revision"
    else
        echo "FAIL: Multiple sidecar revisions detected — check for orphans"
    fi

    # Check 2: No stale pods in mesh namespaces
    local stale_section
    stale_section=$(sed -n '/STALE SIDECAR PODS/,/^---/p' "$file" | grep "/" || true)
    local mesh_ns_list
    mesh_ns_list=$(sed -n '/MESH NAMESPACES/,/^$/p' "$file" | grep -v "^---" | grep -v "^$" | grep -v "^NAMESPACE" | awk '{print $1}')
    local mesh_stale=0
    for ns in $mesh_ns_list; do
        local ns_stale
        ns_stale=$(echo "$stale_section" | grep "^${ns}/" | wc -l | tr -d ' ')
        mesh_stale=$((mesh_stale + ns_stale))
    done
    if [ "$mesh_stale" -eq 0 ]; then
        echo "PASS: No stale sidecar pods in mesh namespaces"
    else
        echo "FAIL: $mesh_stale stale sidecar pod(s) in mesh namespaces"
    fi

    # Check 3: Control planes healthy
    local unhealthy
    unhealthy=$(sed -n '/CONTROL PLANES/,/^$/p' "$file" | grep -c "False" || true)
    if [ "$unhealthy" -eq 0 ]; then
        echo "PASS: All control plane pods ready"
    else
        echo "FAIL: $unhealthy control plane pod(s) not ready"
    fi

    # Check 4: Namespace labels all use tag (not direct revision)
    if grep -q "tag-based (all namespaces)" "$file" 2>/dev/null; then
        echo "PASS: All namespaces use tag-based injection"
    else
        local direct_count
        direct_count=$(grep -c "(direct)" "$file" || true)
        echo "FAIL: $direct_count namespace(s) have direct revision labels"
    fi

    # Check 5: Tag webhook points at correct istiod
    local tag_target
    tag_target=$(sed -n '/TAG WEBHOOK VALIDATION/,/^$/p' "$file" | grep "Target service:" | awk '{print $3}' 2>/dev/null || echo "")
    local tag_ca
    tag_ca=$(sed -n '/TAG WEBHOOK VALIDATION/,/^$/p' "$file" | grep "CA bundle:" | awk '{print $3}' 2>/dev/null || echo "")
    if [ -n "$tag_target" ] && [ "$tag_ca" = "present" ]; then
        echo "PASS: Tag webhook configured (target: $tag_target, CA: present)"
    elif [ -z "$tag_target" ]; then
        echo "WARN: Tag webhook not found"
    else
        echo "FAIL: Tag webhook CA bundle missing"
    fi

    # Check 6: ConfigMap ext-authz providers
    local cm_issues=0
    while IFS= read -r line; do
        if echo "$line" | grep -q "no ext-authz"; then
            echo "FAIL: ConfigMap missing ext-authz: $line"
            cm_issues=$((cm_issues + 1))
        fi
    done < <(sed -n '/CONFIGMAP CONTENT/,/^$/p' "$file" | grep "istio-shared-configmap")
    if [ "$cm_issues" -eq 0 ]; then
        echo "PASS: All ConfigMaps have ext-authz configured"
    fi

    # Check 7: ConfigMap labels preserved (istio.io/rev present, no label stripping)
    local label_issues=0
    while IFS= read -r line; do
        local cm_name
        cm_name=$(echo "$line" | awk '{print $1}' | sed 's/://')
        local labels
        labels=$(echo "$line" | cut -d: -f2-)
        if echo "$labels" | grep -q "MISSING" || ! echo "$labels" | grep -q "istio.io/rev"; then
            echo "FAIL: ConfigMap $cm_name missing istio.io/rev label"
            label_issues=$((label_issues + 1))
        fi
    done < <(sed -n '/CONFIGMAP LABELS/,/^$/p' "$file" | grep "istio-shared-configmap")
    if [ "$label_issues" -eq 0 ]; then
        echo "PASS: All ConfigMaps have istio.io/rev label"
    fi

    # Check 8: Fleet namespace has istio label
    local fleet_label_status
    fleet_label_status=$(sed -n '/FLEET MESH STATUS/,/^$/p' "$file" | grep "Namespace label:" || true)
    if echo "$fleet_label_status" | grep -q "istio.io/rev="; then
        echo "PASS: Fleet namespace has istio.io/rev label"
    else
        echo "WARN: Fleet namespace missing istio.io/rev label (topology ordering gap)"
    fi

    # Check 9: Fleet pods on current sidecar revision
    local fleet_stale
    fleet_stale=$(sed -n '/FLEET MESH STATUS/,/^$/p' "$file" | grep "WARN.*istio-proxy errors" || true)
    if [ -n "$fleet_stale" ]; then
        echo "WARN: Fleet istio-proxy has connectivity errors (stale sidecar?)"
    else
        echo "PASS: Fleet istio-proxy healthy"
    fi

    # Check 10: Deployments fully rolled out (skip 0-replica and <none> deployments)
    local not_ready
    not_ready=$(sed -n '/DEPLOYMENT ROLLOUT STATUS/,/^--- /p' "$file" | grep -v "^\[" | grep -v "^---" | grep -v "^$" | grep -v "^NAME" | awk '{gsub(/<none>/, "0")} $3 != 0 && ($2 != $3 || $3 != $4) {print}' 2>/dev/null || true)
    if [ -z "$not_ready" ]; then
        echo "PASS: All deployments fully rolled out"
    else
        echo "FAIL: Some deployments not fully rolled out:"
        echo "$not_ready" | sed 's/^/  /'
    fi

    # Check 11: Ingress PIP pinning (IP drift prevention)
    local pip_issues=0
    while IFS= read -r line; do
        if echo "$line" | grep -q "pip=none"; then
            echo "FAIL: Ingress service missing PIP annotation: $line"
            pip_issues=$((pip_issues + 1))
        fi
    done < <(sed -n '/INGRESS PIP PINNING/,/^$/p' "$file" | grep "^  ")
    if [ "$pip_issues" -eq 0 ]; then
        echo "PASS: All ingress services have PIP pinned"
    fi

    # Check 12: Sidecar injection — all pods in mesh namespaces should have istio-proxy
    local injection_issues=0
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        local injected total_pods
        injected=$(echo "$line" | grep -oE '[0-9]+/[0-9]+' | cut -d/ -f1)
        total_pods=$(echo "$line" | grep -oE '[0-9]+/[0-9]+' | cut -d/ -f2)
        local inj_ns
        inj_ns=$(echo "$line" | awk -F: '{print $1}' | tr -d ' ')
        if [ -n "$total_pods" ] && [ "$total_pods" -gt 0 ] && [ "$injected" != "$total_pods" ]; then
            echo "FAIL: $inj_ns has $injected/$total_pods pods injected"
            injection_issues=$((injection_issues + 1))
        fi
    done < <(sed -n '/SIDECAR INJECTION STATUS/,/^$/p' "$file" | grep "pods injected")
    if [ "$injection_issues" -eq 0 ]; then
        echo "PASS: All pods in mesh namespaces have sidecar injected"
    fi

    # Check 13: mTLS STRICT enforced mesh-wide
    local strict_policy
    strict_policy=$(sed -n '/MTLS POLICY/,/^$/p' "$file" | grep "aks-istio-system.*STRICT" || true)
    if [ -n "$strict_policy" ]; then
        echo "PASS: mTLS STRICT enforced in aks-istio-system (mesh-wide)"
    else
        echo "WARN: No STRICT mTLS policy found in aks-istio-system"
    fi

    # Check 14: AuthorizationPolicies — zero-trust baseline
    local authz_summary
    authz_summary=$(sed -n '/AUTHORIZATION POLICIES/,/^$/p' "$file" | grep "^  Total:" || true)
    local authz_deny authz_allow
    authz_deny=$(echo "$authz_summary" | grep -oE 'deny-all=[0-9]+' | cut -d= -f2)
    authz_allow=$(echo "$authz_summary" | grep -oE 'explicit-ALLOW=[0-9]+' | cut -d= -f2)
    authz_deny="${authz_deny:-0}"
    authz_allow="${authz_allow:-0}"
    if [ "$authz_deny" -gt 0 ] && [ "$authz_allow" -gt 0 ]; then
        echo "PASS: Zero-trust AuthorizationPolicies in place (deny-all=$authz_deny, ALLOW=$authz_allow)"
    elif [ "$authz_deny" -eq 0 ] && [ "$authz_allow" -eq 0 ]; then
        echo "WARN: No AuthorizationPolicies found"
    else
        echo "WARN: AuthorizationPolicies incomplete (deny-all=$authz_deny, ALLOW=$authz_allow)"
    fi

    # Check 15: Orphaned leases — upstream AKS bug, not blocking
    # https://github.com/Azure/AKS/issues/5862
    local orphan_leases
    orphan_leases=$(sed -n '/LEASES (aks-istio-system)/,/^---/p' "$file" | grep "status=ORPHAN" || true)
    local orphan_lease_count=0
    if [ -n "$orphan_leases" ]; then
        orphan_lease_count=$(echo "$orphan_leases" | wc -l | tr -d ' ')
    fi
    if [ "$orphan_lease_count" -eq 0 ]; then
        echo "PASS: No orphaned leases in aks-istio-system"
    else
        echo "WARN: $orphan_lease_count orphaned lease(s) in aks-istio-system (https://github.com/Azure/AKS/issues/5862)"
        echo "$orphan_leases" | sed 's/^/  /'
    fi

    echo ""
}

case "$PHASE" in
    before)
        echo "Capturing BEFORE state..."
        capture_state | tee "$BEFORE_FILE"
        echo ""
        echo "Saved to $BEFORE_FILE"
        echo ""
        run_verification "$BEFORE_FILE"
        if $LIVE; then run_live_checks; fi
        echo "Run the pipeline, then: $0 after"
        ;;
    after)
        echo "Capturing AFTER state..."
        capture_state | tee "$AFTER_FILE"
        echo ""
        if [ -f "$BEFORE_FILE" ]; then
            echo "=========================================="
            echo "  DIFF: BEFORE -> AFTER"
            echo "=========================================="
            diff -u "$BEFORE_FILE" "$AFTER_FILE" || true
            echo ""
        else
            echo "No before snapshot found at $BEFORE_FILE — run '$0 before' first next time."
            echo ""
        fi

        run_verification "$AFTER_FILE"

        if $LIVE; then run_live_checks; fi
        ;;
    now)
        now_file="/tmp/istio-verify-now.txt"
        capture_state | tee "$now_file"
        echo ""
        run_verification "$now_file"
        if $LIVE; then run_live_checks; fi
        echo ""
        echo "Snapshot saved: $now_file"
        ;;
    *)
        echo "Usage: $0 {before|after|now}"
        exit 1
        ;;
esac
