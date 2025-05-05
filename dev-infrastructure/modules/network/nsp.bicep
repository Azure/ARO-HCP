param location string

@description('The Name the NSP should have')
param nspName string

resource nsp 'Microsoft.Network/networkSecurityPerimeters@2024-06-01-preview' = {
  location: location
  name: nspName
}
