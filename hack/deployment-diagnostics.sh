#!/bin/bash

set -euo pipefail

if [[ -z "${EV2:-}" ]]; then
   # this script only executes in EV2
   # executing this in other environments with less restricted access to CD logs
   # can lead to leaking sensitive information
   exit 0
fi

HACK_DIR=$(dirname "$(realpath "${BASH_SOURCE[0]}")")


HELM_RELEASE_NAME="$1"
NAMESPACE="$2"

cat <<'EOF'


  _          _                      _                       _        __
 | |__   ___| |_ __ ___    _ __ ___| | ___  __ _ ___  ___  (_)_ __  / _| ___
 | '_ \ / _ \ | '_ ` _ \  | '__/ _ \ |/ _ \/ _` / __|/ _ \ | | '_ \| |_ / _ \
 | | | |  __/ | | | | | | | | |  __/ |  __/ (_| \__ \  __/ | | | | |  _| (_) |
 |_| |_|\___|_|_| |_| |_| |_|  \___|_|\___|\__,_|___/\___| |_|_| |_|_|  \___/


EOF

# get release information as json
RELEASE_INFO="$(helm status "$HELM_RELEASE_NAME" -n "${NAMESPACE}" -o json)"
if [ -z "$RELEASE_INFO" ]; then
  echo "Failed to retrieve Helm release information for release ${HELM_RELEASE_NAME} in namespace ${NAMESPACE}"
  exit 1
fi

HELM_DEPLOYMENT_STATUS="$(jq --raw-output .info.status <<<"${RELEASE_INFO}")"
HELM_DEPLOYMENT_DESCRIPTION="$(jq --raw-output .info.description <<<"${RELEASE_INFO}")"
VALUES="$(jq --raw-output .config <<<"${RELEASE_INFO}")"
echo ""
echo "Status: ${HELM_DEPLOYMENT_STATUS}"
echo "Description: ${HELM_DEPLOYMENT_DESCRIPTION}"
echo "Release values:"
echo "${VALUES}" | jq '.'

cat <<'EOF'


  _    ___            _ _                             _   _
 | | _( _ ) ___    __| (_) __ _  __ _ _ __   ___  ___| |_(_) ___ ___
 | |/ / _ \/ __|  / _` | |/ _` |/ _` | '_ \ / _ \/ __| __| |/ __/ __|
 |   < (_) \__ \ | (_| | | (_| | (_| | | | | (_) \__ \ |_| | (__\__ \
 |_|\_\___/|___/  \__,_|_|\__,_|\__, |_| |_|\___/|___/\__|_|\___|___/
                                |___/


EOF

echo -e "\n--- Pods ---"
kubectl get pods -n "$NAMESPACE" || echo "Could not list pods"

DEPLOYMENTS=$(kubectl get deployments -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
for DEPLOY in $DEPLOYMENTS; do
    echo -e "\n--- Describe Deployment: $DEPLOY ---"
    kubectl describe deployment "$DEPLOY" -n "$NAMESPACE"
done

JOBS=$(kubectl get jobs -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
for JOB in $JOBS; do
    echo -e "\n--- Describe Job: $JOB ---"
    kubectl describe job "$JOB" -n "$NAMESPACE"
done

# TODO - add ready-to-click kusto links once kusto is ready

echo -e "\n--- Troubled Pod logs ---"
PODS=$(kubectl get pods -n "$NAMESPACE" --field-selector=status.phase!=Running,status.phase!=Succeeded -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
for POD in $PODS; do
    "${HACK_DIR}/pod-logs.sh" "$POD" "$NAMESPACE" 100
done

echo -e "\n--- ServiceAccounts in $NAMESPACE ---"
SERVICE_ACCOUNTS=$(kubectl get serviceaccounts -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")

if [ -n "$SERVICE_ACCOUNTS" ]; then
  for SA in $SERVICE_ACCOUNTS; do
    echo -e "\n>>> Describe ServiceAccount: $SA"
    kubectl describe serviceaccount "$SA" -n "$NAMESPACE"
  done
else
  echo "No ServiceAccounts found in namespace $NAMESPACE"
fi

echo -e "\n--- SecretProviderClass in $NAMESPACE ---"
SECRET_PROVIDER_CLASSES=$(kubectl get secretproviderclass -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
if [ -n "$SECRET_PROVIDER_CLASSES" ]; then
  for SPC in $SECRET_PROVIDER_CLASSES; do
    echo -e "\n>>> Describe SecretProviderClass: $SPC"
    kubectl describe secretproviderclass "$SPC" -n "$NAMESPACE"
  done
else
  echo "No SecretProviderClass found in namespace $NAMESPACE"
fi

echo -e "\n--- Events in $NAMESPACE (last 100) ---"
kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' | tail -n 100

echo -e "\n=== Finished diagnostics at $(date) ==="
