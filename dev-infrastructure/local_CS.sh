#!/bin/bash

cd ../uhc-clusters-service/

make db/teardown

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

# Build CS
make cmds

# Setup local DB
make db/setup

# Initialize the DB
./clusters-service init --config-file ./development.yml

# Start CS
./clusters-service serve --config-file development.yml --demo-mode