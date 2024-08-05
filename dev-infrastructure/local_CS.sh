#!/bin/bash

cd ../uhc-clusters-service/

make db/teardown

# Obtain Azure credentials from keyvault
VAULTNAME=service-kv-aro-hcp-dev
CURRENTUSER=$(az ad signed-in-user show --query id -o tsv)
VAULTID=$(az keyvault show --name $VAULTNAME --query id -o tsv)
az role assignment create --role "Key Vault Secrets User" --assignee $CURRENTUSER --scope $VAULTID -o none
az keyvault secret show --vault-name $VAULTNAME --name "aro-hcp-dev-sp-cs" | jq .value -r > azure-creds.json

# Setup the development.yml
cp ./configs/development.yml .

# Update any required empty strings to 'none'
yq -i '(.aws-access-key-id, .aws-secret-access-key, .route53-access-key-id, .route53-secret-access-key, .oidc-access-key-id, .oidc-secret-access-key, .network-verifier-access-key-id, .network-verifier-secret-access-key, .client-id, .client-secret) = "none"' development.yml

# Update provision shards config with new shard
cat <<EOF > ./provision_shards.config
provision_shards:
- id: 1
  hypershift_config: |
    apiVersion: v1
    kind: Config
    clusters:
    - name: default
      cluster:
        server: https://api.hs-sc-81qmsevf0.dksu.i1.devshift.org:6443
    users:
    - name: default
      user:
        token: ${HYPERSHIFT_INTEGRATION_TOKEN}
    contexts:
    - name: default
      context:
        cluster: default
        user: default
    current-context: default
  status: active
  region: eastus
  cloud_provider: azure
  topology: dedicated
EOF

# Enable the eastus region in cloud region constraints config
yq -i '.cloud_regions |= map(select(.id == "eastus").enabled = true)' configs/cloud-resource-constraints/cloud-region-constraints.yaml

# you can verify the region change with the below
# yq '.cloud_regions[] | select(.id == "eastus")' configs/cloud-resource-constraints/cloud-region-constraints.yaml

# Update region_constraints.config with new cloud provider
cat <<EOF > ./region_constraints.config
cloud_providers:
- name: azure
  regions:
    - name: eastus
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
