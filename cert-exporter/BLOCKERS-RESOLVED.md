# cert-exporter Blockers - Resolution Summary

## Critical Fixes Applied

### 1. ✅ MGMT Metrics Routing to HCP AMW

**Problem**: MGMT cluster cert-exporter runs in `cert-exporter` namespace, so standard namespace-based routing would send all its metrics to Service AMW instead of HCP AMW, even though it monitors `ocm-*` certificates.

**Solution**: Added custom remote write relabeling in `observability/prometheus/deploy/templates/prometheus.yaml`

**Service AMW - drop x509_cert_* with HCP secret namespaces:**
```yaml
writeRelabelConfigs:
  - sourceLabels: [__name__, secret_namespace]
    separator: ;
    regex: 'x509_cert_.+;(ocm-{{ .Values.environment }}.*|open-cluster-management-policies)'
    action: drop
```

**HCP AMW - keep x509_cert_* with HCP secret namespaces:**
```yaml
writeRelabelConfigs:
  - sourceLabels: [__name__, secret_namespace, namespace, hostedcontrolplane]
    separator: ;
    regex: '(x509_cert_.+;(ocm-{{ .Values.environment }}.*|open-cluster-management-policies);.*;.*|[^;]+;[^;]*;ocm-{{ .Values.environment }}[^;]*;.*|[^;]+;[^;]*;.*;ocm-{{ .Values.environment }}.*)'
    action: keep
```

**Result**: 
- SVC cluster `x509_cert_*{secret_namespace="aks-istio-ingress"}` → **Service AMW** ✅
- MGMT cluster `x509_cert_*{secret_namespace=~"ocm-.*"}` → **HCP AMW** ✅
- MGMT cluster `x509_cert_*{secret_namespace="open-cluster-management-policies"}` → **HCP AMW** ✅

---

### 2. ✅ SVC Scope Narrowed to Required Secrets

**Problem**: SVC configmap used wildcard `include: ["*"]`, collecting every TLS secret in `aks-istio-ingress` instead of only the 3 specified credentials.

**Solution**: Updated `cert-exporter/svc/deploy/templates/configmap.yaml`

**Before:**
```yaml
secrets:
  include: ["*"]
```

**After:**
```yaml
secrets:
  include:
    - "frontend-credential"
    - "admin-api-credential"
    - "sessiongate-credential"
```

**Result**: Only the 3 acceptance-criteria secrets are monitored ✅

---

### 3. ✅ Validation Script Fixed

**Problems**:
- Looked for Service `cert-exporter` instead of `cert-exporter-metrics`
- Examples used wrong namespace (`aks-istio-ingress` for exporter location)
- Incorrect AMW routing guidance

**Solution**: Fixed `cert-exporter/validate-metrics.sh` and `cert-exporter/VALIDATION.md`

**Corrected**:
- Service name: `cert-exporter-metrics`
- Exporter namespace: `cert-exporter` (both SVC and MGMT)
- Watch namespace (SVC): `aks-istio-ingress`
- Watch namespaces (MGMT): `ocm-*` and `open-cluster-management-policies`
- AMW routing:
  - SVC metrics → Service AMW
  - MGMT metrics → HCP AMW (via `secret_namespace` relabeling)

---

## Updated Acceptance Criteria Status

| Criterion | Status | Notes |
|-----------|--------|-------|
| **Exporter deployed** | ✅ Ready | Both SVC and MGMT clusters |
| **ServiceMonitor configured** | ✅ Ready | 30s scrape interval |
| **Metrics exposed** | ✅ Ready | `x509_cert_not_before`, `x509_cert_not_after` |
| **Labels present** | ✅ Ready | `secret_name`, `secret_namespace` + extras |
| **SVC exact scope** | ✅ **FIXED** | Only 3 specified credentials |
| **SVC → Service AMW** | ✅ Ready | Default routing (secret_namespace != ocm-*) |
| **MGMT → HCP AMW** | ✅ **FIXED** | Custom `secret_namespace` relabeling |
| **Collection config** | ✅ **FIXED** | Remote write relabeling in prometheus.yaml |
| **Rotation alerts** | ⏸️ Deferred | Can be added later |

---

## Prometheus Configuration Applied

**File**: `observability/prometheus/deploy/templates/prometheus.yaml`

**Service AMW writeRelabelConfigs** (lines 127-143):
```yaml
writeRelabelConfigs:
  # Drop x509_cert_* metrics with HCP secret namespaces from Service AMW
  - sourceLabels: [__name__, secret_namespace]
    separator: ;
    regex: 'x509_cert_.+;(ocm-{{ .Values.environment }}.*|open-cluster-management-policies)'
    action: drop
  # Existing namespace-based rules
  - sourceLabels: [namespace]
    regex: '^ocm-{{ .Values.environment }}.*'
    action: drop
  - sourceLabels: [hostedcontrolplane]
    regex: '^ocm-{{ .Values.environment }}.*'
    action: drop
```

**HCP AMW writeRelabelConfigs** (lines 161-165):
```yaml
writeRelabelConfigs:
  # Keep x509_cert_* metrics with HCP secret namespaces OR standard HCP metrics
  - sourceLabels: [__name__, secret_namespace, namespace, hostedcontrolplane]
    separator: ;
    regex: '(x509_cert_.+;(ocm-{{ .Values.environment }}.*|open-cluster-management-policies);.*;.*|[^;]+;[^;]*;ocm-{{ .Values.environment }}[^;]*;.*|[^;]+;[^;]*;.*;ocm-{{ .Values.environment }}.*)'
    action: keep
```

---

## What's Left

### Before Deployment:
1. Run tests: `make test` (already passing)
2. Materialize config: `cd config && make materialize`
3. Build and push images
4. Deploy to dev environment

### After Deployment (Runtime Validation):
1. Run validation script:
   ```bash
   # SVC cluster
   ./cert-exporter/validate-metrics.sh cert-exporter svc
   
   # MGMT cluster
   ./cert-exporter/validate-metrics.sh cert-exporter mgmt
   ```

2. Query Azure Monitor Workspaces:
   ```promql
   # Service AMW - should show SVC cluster certs only
   x509_cert_not_after{secret_namespace="aks-istio-ingress"}
   
   # HCP AMW - should show MGMT cluster certs only
   x509_cert_not_after{secret_namespace=~"ocm-.*"}
   x509_cert_not_after{secret_namespace="open-cluster-management-policies"}
   ```

3. Verify counts:
   ```promql
   # SVC: should return 3 (frontend, admin-api, sessiongate)
   count(x509_cert_not_after{secret_namespace="aks-istio-ingress"})
   
   # MGMT: should return N = number of hosted clusters + policy certs
   count(x509_cert_not_after{secret_namespace=~"ocm-.*"})
   ```

### Future Work (Non-Blocking):
- PrometheusRule alerts for expiration/rotation failures
- Grafana dashboard for certificate monitoring
- SRE runbooks for certificate rotation procedures

---

## Files Changed

1. `observability/prometheus/deploy/templates/prometheus.yaml` - Remote write relabeling for x509_cert_* ✅
2. `cert-exporter/svc/deploy/templates/configmap.yaml` - Scoped to 3 secrets ✅
3. `cert-exporter/validate-metrics.sh` - Fixed service name and namespace ✅
4. `cert-exporter/VALIDATION.md` - Corrected architecture and examples ✅
5. All cert-exporter implementation files (RBAC controller, deployment, etc.) ✅

---

## Testing the Fix

After deploying, verify routing with these queries:

**In Service AMW (should ONLY see SVC certs):**
```promql
x509_cert_not_after
# Expected: Only aks-istio-ingress secrets (3 total)
```

**In HCP AMW (should ONLY see MGMT certs):**
```promql
x509_cert_not_after
# Expected: Only ocm-* and open-cluster-management-policies secrets
```

**Expected Result (WITH routing fix applied)**:
- Service AMW: Only SVC certs (`secret_namespace="aks-istio-ingress"`)
- HCP AMW: Only MGMT certs (`secret_namespace=~"ocm-.*|open-cluster-management-policies"`)

**If you see cross-contamination**, the relabeling isn't working:
1. Check Prometheus config rendered correctly
2. Verify `.Values.environment` is set in Helm values
3. Confirm Prometheus reloaded after config change
4. Check Prometheus logs for relabeling errors
