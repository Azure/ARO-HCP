using '../templates/opstool-network.bicep'

param aksClusterName = '{{ .opstool.aks.name }}'
param gatewayEnabled = '{{ .opstool.gateway.enabled }}' == 'true'
param publicIpName = '{{ .opstool.gateway.publicIpName }}'
param parentZoneName = '{{ .opstool.gateway.dns.parentZoneName }}'
param parentZoneResourceGroupName = '{{ .opstool.gateway.dns.parentZoneResourceGroup }}'
param childZoneSubdomain = '{{ .opstool.gateway.dns.childZoneSubdomain }}'
param recordName = '{{ .opstool.gateway.dns.recordName }}'
