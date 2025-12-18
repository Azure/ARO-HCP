az stack sub create \
    --name aro-hcp-msi-pool \
    --location westus3 \
    --template-file test/e2e-setup/bicep/msi-pools.bicep \
    --parameters poolSize=20 \
    --deny-settings-mode None \
    --action-on-unmanage deleteResources
