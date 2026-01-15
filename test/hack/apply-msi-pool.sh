az stack sub create \
    --name aro-hcp-msi-pool \
    --location westus3 \
    --template-file test/e2e-setup/bicep/msi-pools.bicep \
    --parameters poolSize=120 resourceGroupBaseName=aro-hcp-test-msi-containers-dev \
    --deny-settings-mode None \
    --action-on-unmanage deleteResources
