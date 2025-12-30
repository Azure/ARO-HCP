#!/bin/bash
set -e

# This script adds the required OCM addon CRDs that are not included in the MCE bundle
# but are needed by the MCE operator and policy components

CRD_DIR="deploy/helm/multicluster-engine-crds/templates"
TMP_DIR="/tmp/ocm-crds"

mkdir -p "$TMP_DIR"

echo "Downloading OCM addon CRDs..."

# Download the required CRDs from the OCM API repository
curl -sL https://raw.githubusercontent.com/open-cluster-management-io/api/refs/heads/main/addon/v1alpha1/0000_01_addon.open-cluster-management.io_managedclusteraddons.crd.yaml \
  -o "$CRD_DIR/managedclusteraddons.addon.open-cluster-management.io.customresourcedefinition.yaml"

curl -sL https://raw.githubusercontent.com/open-cluster-management-io/api/refs/heads/main/addon/v1alpha1/0000_00_addon.open-cluster-management.io_clustermanagementaddons.crd.yaml \
  -o "$CRD_DIR/clustermanagementaddons.addon.open-cluster-management.io.customresourcedefinition.yaml"

curl -sL https://raw.githubusercontent.com/open-cluster-management-io/api/refs/heads/main/addon/v1alpha1/0000_02_addon.open-cluster-management.io_addondeploymentconfigs.crd.yaml \
  -o "$CRD_DIR/addondeploymentconfigs.addon.open-cluster-management.io.customresourcedefinition.yaml"

echo "OCM addon CRDs added successfully"
