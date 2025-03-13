using '../templates/mgmt-nsp.bicep'

// CX KV
param cxKeyVaultName = 'ah-dev-cx-usw3-1'

// MSI KV
param msiKeyVaultName = 'ah-dev-mi-usw3-1'

// MGMT KV
param mgmtKeyVaultName = 'ah-dev-mg-usw3-1'

// ETCD KV
param aksKeyVaultName = 'ah-dev-me-usw3-1'


param mgmtNSPName = 'nsp-usw3-mgmt-1'
param mgmtNSPAccessMode = 'Learning'

param serviceClusterSubscriptionId = '__serviceClusterSubscriptionId__'
