# Certificate Exporter - AMW Metrics Validation Guide

This guide walks through validating that cert-exporter metrics are flowing from Kubernetes to Azure Monitor Workspace (AMW).

## Metrics Flow Architecture

```
cert-exporter Pod (port 9793)
    ↓
Service: cert-exporter-metrics
    ↓
ServiceMonitor (scrape every 30s)
    ↓
Self-Managed Prometheus
    ↓ (remote write with metric-based relabeling)
    ├─ Service AMW ← SVC cluster x509_cert_* (secret_namespace: aks-istio-ingress)
    └─ HCP AMW ← MGMT cluster x509_cert_* (secret_namespace: ocm-* or open-cluster-management-policies)
```

**Key Understanding**:
- **SVC cluster**: cert-exporter in `cert-exporter` namespace watches secrets in `aks-istio-ingress`
  - Metrics route to **Service AMW** (secret_namespace doesn't match ocm-*)
- **MGMT cluster**: cert-exporter in `cert-exporter` namespace watches secrets in `ocm-*` and `open-cluster-management-policies`
  - Metrics route to **HCP AMW** based on `secret_namespace` label, NOT the exporter's namespace
- **Routing uses custom relabeling**: `x509_cert_*` metrics are routed by `secret_namespace` label instead of `namespace`

---

## Prerequisites

Before validating, ensure you have:

1. **Azure CLI** authenticated to the correct environment
2. **kubectl** context set to the target cluster (SVC or MGMT)
3. **Azure Portal access** or **Grafana access** to the environment
4. **Permission** to query Azure Monitor Workspaces

---

## Validation Steps

### Step 1: Verify cert-exporter Deployment

#### 1.1 Check Pod Status

**Both SVC and MGMT Clusters:**
```bash
kubectl get pods -n cert-exporter -l app.kubernetes.io/name=cert-exporter
```

**MGMT Cluster Only (additional RBAC controller):**
```bash
kubectl get pods -n cert-exporter -l app.kubernetes.io/name=cert-exporter-rbac-controller
```

**Expected Output:**
```
NAME                            READY   STATUS    RESTARTS   AGE
cert-exporter-xxxxxxxxxx-xxxxx   1/1     Running   0          10m
```

#### 1.2 Check ServiceMonitor Discovery

```bash
kubectl get servicemonitor -n cert-exporter cert-exporter -o yaml
```

**Verify:**
- `spec.endpoints[0].path: /metrics`
- `spec.endpoints[0].port: metrics`
- `spec.endpoints[0].interval: 30s`

---

### Step 2: Validate Local Metrics Exposure

Port-forward to the cert-exporter pod and verify metrics are exposed:

```bash
# Get pod name
POD=$(kubectl get pods -n cert-exporter -l app.kubernetes.io/name=cert-exporter -o jsonpath='{.items[0].metadata.name}')

# Port forward
kubectl port-forward -n cert-exporter pod/$POD 9793:9793 &

# Query metrics
curl -s http://localhost:9793/metrics | grep x509_cert
```

**Expected Output:**
```
# HELP x509_cert_not_after Timestamp after which certificate is invalid
# TYPE x509_cert_not_after gauge
x509_cert_not_after{secret_name="kube-apiserver-tls-cert",secret_namespace="ocm-arohcp-dev-cluster-001",...} 1.7567136e+09

# HELP x509_cert_not_before Timestamp before which certificate is invalid
# TYPE x509_cert_not_before gauge
x509_cert_not_before{secret_name="kube-apiserver-tls-cert",secret_namespace="ocm-arohcp-dev-cluster-001",...} 1.7251584e+09

# HELP x509_cert_expired Certificate is expired (1 = expired, 0 = valid)
# TYPE x509_cert_expired gauge
x509_cert_expired{secret_name="kube-apiserver-tls-cert",secret_namespace="ocm-arohcp-dev-cluster-001",...} 0
```

**Verify Labels Present:**
- ✅ `secret_name` (e.g., `kube-apiserver-tls-cert`, `frontend-credential`)
- ✅ `secret_namespace` (e.g., `ocm-*`, `aks-istio-ingress`)
- ✅ Additional: `secret_key`, `subject_CN`, `issuer_CN`, `serial_number`

```bash
# Kill port-forward when done
pkill -f "port-forward.*9793"
```

---

### Step 3: Verify Prometheus Scraping

Check if Prometheus is discovering and scraping the cert-exporter target.

#### 3.1 Port-Forward to Prometheus

```bash
# MGMT cluster example
kubectl port-forward -n prometheus svc/prometheus-operated 9090:9090 &

# Open browser to http://localhost:9090
```

#### 3.2 Check Targets in Prometheus UI

Navigate to: **Status → Targets**

Search for: `cert-exporter`

**Verify:**
- State: **UP** (green)
- Endpoint: `http://cert-exporter-metrics.cert-exporter.svc.cluster.local:9793/metrics`
- Last Scrape: < 30 seconds ago
- Health: ✅

#### 3.3 Query Metrics in Prometheus

Navigate to: **Graph**

Test queries:
```promql
# Check if metrics exist
x509_cert_not_after

# SVC cluster - frontend credential
x509_cert_not_after{secret_namespace="aks-istio-ingress",secret_name="frontend-credential"}

# MGMT cluster - apiserver certs
x509_cert_not_after{secret_namespace=~"ocm-.*",secret_name="kube-apiserver-tls-cert"}

# MGMT cluster - ingress certs
x509_cert_not_after{secret_namespace="open-cluster-management-policies",secret_name=~"default-ingress-tls-cert-.*"}
```

**Expected Result:** Non-empty result set with recent timestamps

```bash
# Kill port-forward
pkill -f "port-forward.*9090"
```

---

### Step 4: Verify Remote Write to Azure Monitor Workspace

#### 4.1 Get AMW Details

Find the Azure Monitor Workspace for your environment:

```bash
# Set environment variables
RESOURCE_GROUP="<region>-hcp"  # e.g., westus3-hcp
ENVIRONMENT="dev"  # or int, stg, prod

# List AMWs in resource group
az monitor account list \
  --resource-group $RESOURCE_GROUP \
  --output table

# Expected: Two workspaces
# - arohcp-<env>-<region>-services  (Service AMW)
# - arohcp-<env>-<region>-hcps      (HCP AMW)
```

**For cert-exporter metrics (after relabeling fix):**
- **SVC cluster** → Query **Service AMW** (`*-services`) - `secret_namespace=aks-istio-ingress` doesn't match ocm-*
- **MGMT cluster** → Query **HCP AMW** (`*-hcps`) - `secret_namespace=ocm-*` or `open-cluster-management-policies` routes to HCP workspace

#### 4.2 Check Prometheus Remote Write Status

```bash
# Port-forward to Prometheus again
kubectl port-forward -n prometheus svc/prometheus-operated 9090:9090 &

# Check remote write status
curl -s http://localhost:9090/api/v1/status/config | jq '.data.yaml' | grep -A 20 "remoteWrite"

# Check remote write metrics
curl -s http://localhost:9090/metrics | grep prometheus_remote_storage
```

**Key Metrics to Check:**
- `prometheus_remote_storage_samples_total` (should be increasing)
- `prometheus_remote_storage_succeeded_samples_total` (should match samples_total)
- `prometheus_remote_storage_failed_samples_total` (should be 0 or low)

```bash
pkill -f "port-forward.*9090"
```

---

### Step 5: Query Metrics in Azure Monitor Workspace

#### 5.1 Via Azure Portal

1. Navigate to **Azure Portal** → **Azure Monitor Workspaces**
2. Select the **Service AMW** (e.g., `arohcp-dev-westus3-services`)
3. Go to **Metrics Explorer** or **Prometheus Explorer**
4. Run PromQL queries:

```promql
# Check if cert-exporter metrics exist
x509_cert_not_after

# SVC cluster - verify frontend credential
x509_cert_not_after{secret_namespace="aks-istio-ingress", secret_name="frontend-credential"}

# MGMT cluster - verify apiserver certs
x509_cert_not_after{secret_namespace=~"ocm-.*", secret_name="kube-apiserver-tls-cert"}

# Count certificates being monitored
count(x509_cert_not_after)

# Check for soon-to-expire certificates (< 30 days)
(x509_cert_not_after - time()) / 86400 < 30
```

#### 5.2 Via Grafana

1. Navigate to **Azure Managed Grafana** for your environment
2. Create a new dashboard panel or use **Explore**
3. Select data source:
   - **SVC cluster**: Azure Monitor Workspace - Services
   - **MGMT cluster**: Azure Monitor Workspace - HCPs
4. Query Type: **PromQL**
5. Run the same queries as above

---

### Step 6: Validate Certificate Lifecycle Metrics

Create a test query to validate all target certificates are present:

```promql
# SVC cluster - should have 3+ certificates
count(x509_cert_not_after{secret_namespace="aks-istio-ingress"})

# MGMT cluster - apiserver certs (one per hosted cluster)
count(x509_cert_not_after{secret_namespace=~"ocm-.*", secret_name="kube-apiserver-tls-cert"})

# MGMT cluster - ingress certs
count(x509_cert_not_after{secret_namespace="open-cluster-management-policies", secret_name=~"default-ingress-tls-cert-.*"})
```

**Verify expiry timeline:**
```promql
# Days until expiration
(x509_cert_not_after - time()) / 86400

# Certificates expiring in next 30 days
(x509_cert_not_after - time()) / 86400 < 30 and (x509_cert_not_after - time()) / 86400 > 0
```

---

## Troubleshooting

### Metrics Not Appearing in AMW

**Symptom:** Metrics visible in Prometheus but not in AMW

**Diagnosis:**
```bash
# Check Prometheus remote write errors
kubectl logs -n prometheus -l app.kubernetes.io/name=prometheus --tail=100 | grep -i "remote_write\|error"

# Check Data Collection Rule association
az monitor data-collection rule show \
  --name <dcr-name> \
  --resource-group $RESOURCE_GROUP
```

**Common Causes:**
1. **Workload Identity not configured** → Check pod identity
2. **DCR permissions missing** → Verify "Monitoring Metrics Publisher" role
3. **Network policy blocking** → Check if prometheus can reach DCE

### ServiceMonitor Not Discovered

**Symptom:** cert-exporter not showing in Prometheus targets

**Diagnosis:**
```bash
# Check ServiceMonitor exists
kubectl get servicemonitor -A | grep cert-exporter

# Check Prometheus operator logs
kubectl logs -n prometheus -l app.kubernetes.io/name=prometheus-operator

# Verify selector matches
kubectl get servicemonitor -n cert-exporter cert-exporter -o yaml | grep -A 5 selector
kubectl get svc -n cert-exporter cert-exporter -o yaml | grep -A 3 labels
```

### Cert-Exporter Not Exposing Metrics

**Symptom:** `curl` to `/metrics` fails or returns no x509 metrics

**Diagnosis:**
```bash
# Check pod logs
kubectl logs -n cert-exporter -l app.kubernetes.io/name=cert-exporter

# Check if secrets exist
kubectl get secrets -n aks-istio-ingress --field-selector type=kubernetes.io/tls
kubectl get secrets -n ocm-* --field-selector type=kubernetes.io/tls | grep kube-apiserver

# Check RBAC permissions (MGMT cluster)
kubectl get rolebinding -n ocm-<cluster-name> cert-exporter -o yaml
```

---

## Expected Results Summary

✅ **Step 1:** cert-exporter pods Running (1/1)  
✅ **Step 2:** `/metrics` endpoint returns `x509_cert_not_after` with proper labels  
✅ **Step 3:** Prometheus shows target as UP, metrics queryable  
✅ **Step 4:** Remote write succeeds (check metrics)  
✅ **Step 5:** AMW/Grafana shows cert-exporter metrics  
✅ **Step 6:** All target certificates present with expiry data  

---

## Next Steps

Once metrics are validated:

1. **Create PrometheusRule alerts** (see [../observability/alerts/](../observability/alerts/))
2. **Build Grafana dashboard** for certificate expiry monitoring
3. **Document runbook** for SRE certificate rotation procedures
4. **Set up IcM integration** for critical expiry alerts

---

## Quick Validation Script

```bash
#!/bin/bash
set -e

NAMESPACE="${1:-cert-exporter}"
CLUSTER_TYPE="${2:-mgmt}"  # svc or mgmt

echo "🔍 Validating cert-exporter in namespace: $NAMESPACE"

# 1. Check pod
echo "✓ Checking pod status..."
kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=cert-exporter

# 2. Check ServiceMonitor
echo "✓ Checking ServiceMonitor..."
kubectl get servicemonitor -n $NAMESPACE cert-exporter

# 3. Port-forward and test metrics
echo "✓ Testing metrics endpoint..."
POD=$(kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=cert-exporter -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n $NAMESPACE pod/$POD 9793:9793 &
PF_PID=$!
sleep 2

METRIC_COUNT=$(curl -s http://localhost:9793/metrics | grep -c "^x509_cert_not_after{" || echo "0")
echo "   Found $METRIC_COUNT x509_cert_not_after metrics"

kill $PF_PID 2>/dev/null || true

if [ "$METRIC_COUNT" -gt 0 ]; then
  echo "✅ Local validation PASSED"
  echo ""
  echo "Next: Verify metrics in Azure Monitor Workspace"
  echo "  1. Open Azure Portal → Azure Monitor Workspaces"
  echo "  2. Select Service AMW: arohcp-<env>-<region>-services"
  echo "  3. Query: x509_cert_not_after"
else
  echo "❌ Local validation FAILED - no metrics found"
  exit 1
fi
```

**Usage:**
```bash
# MGMT cluster
./validate-cert-exporter.sh cert-exporter mgmt

# SVC cluster  
./validate-cert-exporter.sh aks-istio-ingress svc
```
