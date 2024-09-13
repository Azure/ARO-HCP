#!/bin/bash

cd ../uhc-clusters-service/

make db/teardown

# Obtain Azure credentials from keyvault
VAULTNAME=service-kv-aro-hcp-dev
az keyvault secret show --vault-name $VAULTNAME --name "aro-hcp-dev-pull-secret" | jq .value -r > pull-secret.json
az keyvault secret show --vault-name $VAULTNAME --name "aro-hcp-dev-sp" | jq .value -r > azure-creds
az keyvault secret show --vault-name $VAULTNAME --name "aro-hcp-dev-sp-cs" | jq .value -r > azure-creds.json

# Setup the development.yml
cp ./configs/development.yml .

# Update any required empty strings to 'none'
yq -i '(.aws-access-key-id, .aws-secret-access-key, .route53-access-key-id, .route53-secret-access-key, .oidc-access-key-id, .oidc-secret-access-key, .network-verifier-access-key-id, .network-verifier-secret-access-key, .client-id, .client-secret) = "none"' development.yml

# Generate a provision_shards.config for port-forwarded maestro ...
make -C ../ARO-HCP/cluster-service provision-shard > provision_shards.config

# Enable the westus3 region in cloud region config

cat <<EOF>> ./configs/cloud-resources/cloud-regions.yaml
  - id: westus3
    cloud_provider_id: azure
    display_name: West US 3
    supports_multi_az: true
EOF

cat <<EOF>> ./configs/cloud-resources/cloud-regions-constraints.yaml
  - id: westus3
    enabled: true
    govcloud: false
    ccs_only: true
EOF

# you can verify the region change with the below
# yq '.cloud_regions[] | select(.id == "westus3")' configs/cloud-resource-constraints/cloud-region-constraints.yaml

# Update region_constraints.config with new cloud provider
cat <<EOF > ./region_constraints.config
cloud_providers:
- name: azure
  regions:
    - name: westus3
      version_constraints:
        min_version: 4.11.0
      product_constraints:
        - product: hcp
          version_constraints:
            min_version: 4.12.23
EOF

cat <<EOF > ./configs/cloud-resources/instance-types.yaml
instance_types:
  - id: Standard_D4as_v4
    name: Standard_D4as_v4 - General purpose
    cloud_provider_id: azure
    cpu_cores: 4
    memory: 17179869184
    category: general_purpose
    size: d4as_v4
    generic_name: standard-d4as_v4
EOF

cat <<EOF > ./configs/cloud-resource-constraints/instance-type-constraints.yaml
instance_types:
  - id: Standard_D4as_v4
    ccs_only: true
    enabled: true
EOF

# Build CS
make cmds

# Setup local DB
make db/setup

# Initialize the DB
./clusters-service init --config-file ./development.yml

# Start CS
./clusters-service serve --config-file development.yml --runtime-mode aro-hcp --azure-auth-config-path azure-creds.json
