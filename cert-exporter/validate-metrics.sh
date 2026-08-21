#!/bin/bash
# Copyright 2026 Microsoft Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -e

NAMESPACE="${1:-cert-exporter}"
CLUSTER_TYPE="${2:-mgmt}"  # svc or mgmt

echo "🔍 Validating cert-exporter in namespace: $NAMESPACE (cluster type: $CLUSTER_TYPE)"
echo ""

# 1. Check pod status
echo "📦 Step 1: Checking pod status..."
POD_STATUS=$(kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=cert-exporter --no-headers 2>/dev/null || echo "")
if [ -z "$POD_STATUS" ]; then
  echo "   ❌ No cert-exporter pods found in namespace $NAMESPACE"
  exit 1
fi

POD_NAME=$(echo "$POD_STATUS" | awk '{print $1}' | head -1)
POD_READY=$(echo "$POD_STATUS" | awk '{print $2}' | head -1)
POD_STATE=$(echo "$POD_STATUS" | awk '{print $3}' | head -1)

echo "   Pod: $POD_NAME"
echo "   Ready: $POD_READY"
echo "   Status: $POD_STATE"

if [ "$POD_READY" != "1/1" ] || [ "$POD_STATE" != "Running" ]; then
  echo "   ⚠️  Pod not fully ready"
  kubectl describe pod -n $NAMESPACE $POD_NAME | tail -20
  exit 1
fi
echo "   ✅ Pod is Running and Ready"
echo ""

# 2. Check ServiceMonitor
echo "📊 Step 2: Checking ServiceMonitor..."
SM_EXISTS=$(kubectl get servicemonitor -n $NAMESPACE cert-exporter --no-headers 2>/dev/null || echo "")
if [ -z "$SM_EXISTS" ]; then
  echo "   ❌ ServiceMonitor not found in namespace $NAMESPACE"
  exit 1
fi
echo "   ✅ ServiceMonitor exists: cert-exporter"
SM_INTERVAL=$(kubectl get servicemonitor -n $NAMESPACE cert-exporter -o jsonpath='{.spec.endpoints[0].interval}')
echo "   Scrape interval: $SM_INTERVAL"
echo ""

# 3. Check Service
echo "🌐 Step 3: Checking Service..."
SVC_EXISTS=$(kubectl get svc -n $NAMESPACE cert-exporter-metrics --no-headers 2>/dev/null || echo "")
if [ -z "$SVC_EXISTS" ]; then
  echo "   ❌ Service not found in namespace $NAMESPACE"
  exit 1
fi
SVC_PORT=$(kubectl get svc -n $NAMESPACE cert-exporter-metrics -o jsonpath='{.spec.ports[?(@.name=="metrics")].port}')
echo "   ✅ Service exists: cert-exporter-metrics"
echo "   Metrics port: $SVC_PORT"
echo ""

# 4. Port-forward and test metrics
echo "🔌 Step 4: Testing metrics endpoint (port-forward)..."
kubectl port-forward -n $NAMESPACE pod/$POD_NAME 9793:9793 >/dev/null 2>&1 &
PF_PID=$!
sleep 3

# Test if port-forward is working
if ! curl -s --max-time 5 http://localhost:9793/metrics >/dev/null 2>&1; then
  echo "   ❌ Failed to connect to metrics endpoint"
  kill $PF_PID 2>/dev/null || true
  exit 1
fi

# Get metrics
METRICS_OUTPUT=$(curl -s --max-time 10 http://localhost:9793/metrics)
METRIC_COUNT=$(echo "$METRICS_OUTPUT" | grep -c "^x509_cert_not_after{" || echo "0")
NOT_BEFORE_COUNT=$(echo "$METRICS_OUTPUT" | grep -c "^x509_cert_not_before{" || echo "0")
EXPIRED_COUNT=$(echo "$METRICS_OUTPUT" | grep -c "^x509_cert_expired{" || echo "0")

echo "   Metrics found:"
echo "     x509_cert_not_after: $METRIC_COUNT"
echo "     x509_cert_not_before: $NOT_BEFORE_COUNT"
echo "     x509_cert_expired: $EXPIRED_COUNT"
echo ""

if [ "$METRIC_COUNT" -eq 0 ]; then
  echo "   ❌ No x509_cert_not_after metrics found!"
  echo ""
  echo "   Checking cert-exporter logs for errors..."
  kubectl logs -n $NAMESPACE $POD_NAME --tail=20
  kill $PF_PID 2>/dev/null || true
  exit 1
fi

# Show sample metrics
echo "   📋 Sample metrics:"
echo "$METRICS_OUTPUT" | grep "^x509_cert_not_after{" | head -3 | sed 's/^/     /'
echo ""

# Check for required labels
HAS_SECRET_NAME=$(echo "$METRICS_OUTPUT" | grep -c 'secret_name=' || echo "0")
HAS_SECRET_NS=$(echo "$METRICS_OUTPUT" | grep -c 'secret_namespace=' || echo "0")

if [ "$HAS_SECRET_NAME" -gt 0 ] && [ "$HAS_SECRET_NS" -gt 0 ]; then
  echo "   ✅ Required labels present: secret_name, secret_namespace"
else
  echo "   ⚠️  Missing required labels!"
fi

# Cleanup port-forward
kill $PF_PID 2>/dev/null || true
echo ""

# 5. Check RBAC (MGMT cluster only)
if [ "$CLUSTER_TYPE" = "mgmt" ]; then
  echo "🔐 Step 5: Checking RBAC controller (MGMT cluster)..."
  RBAC_POD=$(kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=cert-exporter-rbac-controller --no-headers 2>/dev/null | awk '{print $1}' || echo "")
  if [ -z "$RBAC_POD" ]; then
    echo "   ⚠️  RBAC controller pod not found"
  else
    RBAC_READY=$(kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=cert-exporter-rbac-controller --no-headers | awk '{print $2}')
    echo "   RBAC controller pod: $RBAC_POD"
    echo "   Ready: $RBAC_READY"

    # Check RoleBindings in ocm-* namespaces
    OCM_NS_COUNT=$(kubectl get ns -l ocm --no-headers 2>/dev/null | wc -l || echo "0")
    if [ "$OCM_NS_COUNT" -gt 0 ]; then
      ROLEBINDING_COUNT=$(kubectl get rolebinding -A -l app.kubernetes.io/managed-by=cert-exporter-rbac-controller --no-headers 2>/dev/null | wc -l || echo "0")
      echo "   RoleBindings managed by controller: $ROLEBINDING_COUNT"

      if [ "$ROLEBINDING_COUNT" -gt 0 ]; then
        echo "   ✅ RBAC controller is managing RoleBindings"
      else
        echo "   ⚠️  No managed RoleBindings found (may need time to reconcile)"
      fi
    fi
  fi
  echo ""
fi

# Final summary
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ Local validation PASSED"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "📈 Next Steps: Verify metrics in Azure Monitor Workspace"
echo ""
echo "Via Azure Portal:"
echo "  1. Navigate to Azure Monitor Workspaces"
echo "  2. Select Service AMW: arohcp-<env>-<region>-services"
echo "  3. Go to 'Prometheus Explorer'"
echo "  4. Run query: x509_cert_not_after"
echo ""
echo "Via Grafana:"
echo "  1. Open Azure Managed Grafana for your environment"
echo "  2. Go to 'Explore'"
echo "  3. Select data source:"
if [ "$CLUSTER_TYPE" = "svc" ]; then
  echo "     - Azure Monitor Workspace - Services (SVC metrics)"
else
  echo "     - Azure Monitor Workspace - HCPs (MGMT metrics from ocm-* namespaces)"
fi
echo "  4. Run query: x509_cert_not_after"
echo ""
echo "Example queries:"
echo "  # All certificates"
echo "  x509_cert_not_after"
echo ""
if [ "$CLUSTER_TYPE" = "svc" ]; then
  echo "  # SVC cluster - frontend credential (watched from aks-istio-ingress)"
  echo "  x509_cert_not_after{secret_namespace=\"aks-istio-ingress\", secret_name=\"frontend-credential\"}"
  echo ""
  echo "  # Count certificates in SVC cluster"
  echo "  count(x509_cert_not_after{secret_namespace=\"aks-istio-ingress\"})"
else
  echo "  # MGMT cluster - apiserver certs"
  echo "  x509_cert_not_after{secret_namespace=~\"ocm-.*\", secret_name=\"kube-apiserver-tls-cert\"}"
  echo ""
  echo "  # MGMT cluster - ingress certs"
  echo "  x509_cert_not_after{secret_namespace=\"open-cluster-management-policies\"}"
  echo ""
  echo "  # Count hosted cluster certificates"
  echo "  count(x509_cert_not_after{secret_namespace=~\"ocm-.*\"})"
fi
echo ""
echo "  # Certificates expiring in < 30 days"
echo "  (x509_cert_not_after - time()) / 86400 < 30"
echo ""
echo "For detailed validation steps, see: cert-exporter/VALIDATION.md"
