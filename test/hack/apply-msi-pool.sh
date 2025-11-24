az stack sub create \
    --name aro-hcp-msi-pool \
    --location uksouth \
    --template-file test/e2e-setup/bicep/msi-pools.bicep \
    --parameters poolSize=1 \
    --deny-settings-mode None \
    --action-on-unmanage deleteResources
