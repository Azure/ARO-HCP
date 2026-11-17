import * as res from '../../dev-infrastructure/modules/resource.bicep'

@description('RP frontend FQDN, unchanged during the Gateway API migration.')
param hostname string

@description('Existing public IP in this regional resource group, selected by configuration.')
param publicIPAddressName string

@description('Only cut over DNS after the frontend Helm readiness hook succeeds.')
param activate bool = false

resource ingressIP 'Microsoft.Network/publicIPAddresses@2024-05-01' existing = {
  name: publicIPAddressName
}

var dns = res.dnsRecordRefFromFqdn(hostname)
module frontendDNS '../../dev-infrastructure/modules/dns/a-record.bicep' = if (activate) {
  name: 'frontend-dns'
  params: {
    zoneName: dns.zoneName
    recordName: dns.recordName
    // ARM may evaluate runtime functions even on a conditional deployment.
    // Do not look up the opt-in public IP while the feature is disabled.
    ipAddress: activate ? ingressIP.properties.ipAddress : ''
    ttl: 300
  }
}
