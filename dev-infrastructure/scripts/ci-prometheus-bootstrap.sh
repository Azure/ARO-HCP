#!/bin/bash
# CI Prometheus Bootstrap Script
# Injects job metadata into Prometheus configuration before deployment
# This script should be sourced by CI jobs before deploying Prometheus

set -euo pipefail

echo "========================================="
echo "CI Prometheus Bootstrap"
echo "========================================="

# Extract CI job metadata from Prow environment variables
export CI_JOB_ID="${PROW_JOB_ID:-unknown}"
export CI_PR_NUMBER="${PULL_NUMBER:-0}"
export CI_GIT_SHA="${PULL_PULL_SHA:-unknown}"
export CI_CLUSTER_NAME="${AKS_CLUSTER_NAME:-ci-unknown}"

echo "CI Job Metadata:"
echo "  Job ID: $CI_JOB_ID"
echo "  PR Number: $CI_PR_NUMBER"
echo "  Git SHA: $CI_GIT_SHA"
echo "  Cluster: $CI_CLUSTER_NAME"

# Get CI DCR URLs from global infrastructure
# These are outputs from the ci-monitoring deployment
echo ""
echo "Fetching CI monitoring endpoints..."

export CI_DCR_URL=$(az deployment group show \
  --resource-group "${GLOBAL_RESOURCE_GROUP}" \
  --name ci-monitoring \
  --query 'properties.outputs.ciDcrRemoteWriteUrl.value' \
  -o tsv 2>/dev/null || echo "")

export CI_HCP_DCR_URL=$(az deployment group show \
  --resource-group "${GLOBAL_RESOURCE_GROUP}" \
  --name ci-monitoring \
  --query 'properties.outputs.ciHcpDcrRemoteWriteUrl.value' \
  -o tsv 2>/dev/null || echo "")

if [ -z "$CI_DCR_URL" ]; then
  echo "ERROR: Could not retrieve CI DCR remote write URL"
  echo "Ensure ci-monitoring deployment exists in resource group: ${GLOBAL_RESOURCE_GROUP}"
  exit 1
fi

echo "Remote Write URLs:"
echo "  Services: $CI_DCR_URL"
echo "  HCP: $CI_HCP_DCR_URL"

# Template the Prometheus values file
CI_VALUES_FILE="observability/prometheus/values-ci-templated.yaml"
CI_VALUES_SOURCE="observability/prometheus/values-ci.yaml"

echo ""
echo "Templating Prometheus values..."
echo "  Source: $CI_VALUES_SOURCE"
echo "  Output: $CI_VALUES_FILE"

if [ ! -f "$CI_VALUES_SOURCE" ]; then
  echo "ERROR: Source values file not found: $CI_VALUES_SOURCE"
  exit 1
fi

cp "$CI_VALUES_SOURCE" "$CI_VALUES_FILE"

# Replace placeholders with actual values
sed -i "s|__CI_JOB_ID__|${CI_JOB_ID}|g" "$CI_VALUES_FILE"
sed -i "s|__CI_PR_NUMBER__|${CI_PR_NUMBER}|g" "$CI_VALUES_FILE"
sed -i "s|__CI_GIT_SHA__|${CI_GIT_SHA}|g" "$CI_VALUES_FILE"
sed -i "s|__ciDcrRemoteWriteUrl__|${CI_DCR_URL}|g" "$CI_VALUES_FILE"
sed -i "s|__ciHcpDcrRemoteWriteUrl__|${CI_HCP_DCR_URL}|g" "$CI_VALUES_FILE"

echo ""
echo "========================================="
echo "✓ Prometheus values templated successfully"
echo "========================================="
echo ""
echo "External labels that will be attached to all metrics:"
echo "  environment: ci"
echo "  cluster: $CI_CLUSTER_NAME"
echo "  job_id: $CI_JOB_ID"
echo "  pr_number: $CI_PR_NUMBER"
echo "  git_sha: $CI_GIT_SHA"
echo ""
echo "These labels enable post-job analysis in Grafana even after cluster deletion."
echo ""
echo "Ready to deploy Prometheus with CI job metadata."
echo "Use: make deploy-prometheus VALUES_FILE=$CI_VALUES_FILE"
echo ""
