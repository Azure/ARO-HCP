#!/bin/bash

# Temporary cleanup script for Package Operator (PKO) removal.
# PKO was previously deployed to management clusters via a Helm chart.
# This script removes all PKO resources. It is idempotent and safe to
# run on clusters that never had PKO installed.
#
# Remove this script (and its pipeline step) once all management clusters
# have been cleaned up.
#
# Tracks: ARO-23308 / AROSLSRE-686

set -o errexit
set -o nounset
set -o pipefail

NAMESPACE="package-operator-system"

echo "Checking for Package Operator (PKO) leftovers..."

# Check if the namespace exists at all
if ! kubectl get namespace "${NAMESPACE}" &>/dev/null; then
  echo "Namespace ${NAMESPACE} does not exist. Nothing to clean up."
  exit 0
fi

echo "Found namespace ${NAMESPACE}, cleaning up PKO..."

# Uninstall the Helm release if it exists
if helm status package-operator -n "${NAMESPACE}" &>/dev/null; then
  echo "Uninstalling Helm release 'package-operator'..."
  helm uninstall package-operator -n "${NAMESPACE}" --wait
  echo "Helm release removed."
else
  echo "No Helm release 'package-operator' found."
fi

# Remove cluster-scoped resources
echo "Removing cluster-scoped resources..."
kubectl delete clusterrolebinding package-operator --ignore-not-found
kubectl delete clusterrole package-operator --ignore-not-found

# Remove the namespace
echo "Removing namespace ${NAMESPACE}..."
kubectl delete namespace "${NAMESPACE}" --ignore-not-found --wait=false

echo "PKO cleanup complete."
