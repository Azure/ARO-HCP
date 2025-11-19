az deployment sub create \
    --name aro-hcp-msi-pool \
    --location eastus \
    --template-file test/e2e-setup/bicep/msi-pool.bicep \
    --parameters poolSize=300 msisPerResourceGroup=13
