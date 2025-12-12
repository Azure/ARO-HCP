#!/bin/bash
# Test local custom-metrics-collector

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

echo "Building custom-metrics-collector..."
make custom-metrics-collector

echo ""
echo "Starting custom-metrics-collector locally..."
echo "Metrics will be available at: http://localhost:8080/metrics"
echo "Health check at: http://localhost:8080/healthz"
echo ""
echo "Press Ctrl+C to stop"
echo ""

# Run with test config
CONFIG_PATH=./hack/test-config-local.yaml PORT=8080 ./custom-metrics-collector

