// bicep func to extract subscription, resourcegroup from a resource id

@export()
type resourceGroupReference = {
  subscriptionId: string
  name: string
}

@export()
func resourceGroupFromResourceId(resourceId string) resourceGroupReference => {
  subscriptionId: split(resourceId, '/')[2]
  name: split(resourceId, '/')[4]
}

@export()
type msiRef = {
  resourceGroup: resourceGroupReference
  name: string
}

@export()
func msiRefFromId(msiResourceId string) msiRef => {
  resourceGroup: resourceGroupFromResourceId(msiResourceId)
  name: last(split(msiResourceId, '/'))
}

@export()
func isMsiResourceId(resourceId string) bool =>
  contains(resourceId, '/providers/Microsoft.ManagedIdentity/userAssignedIdentities/')

@export()
type acrRef = {
  resourceGroup: resourceGroupReference
  name: string
}

@export()
func acrRefFromId(acrResourceId string) acrRef => {
  resourceGroup: resourceGroupFromResourceId(acrResourceId)
  name: last(split(acrResourceId, '/'))
}

@export()
type dnsZoneRef = {
  resourceGroup: resourceGroupReference
  name: string
}

@export()
func dnsZoneRefFromId(dnsZoneResourceId string) dnsZoneRef => {
  resourceGroup: resourceGroupFromResourceId(dnsZoneResourceId)
  name: last(split(dnsZoneResourceId, '/'))
}

@export()
type monitoringWorkspaceRef = {
  resourceGroup: resourceGroupReference
  name: string
}

@export()
func monitoringWorkspaceRefFromId(monitoringWorkspaceResourceId string) monitoringWorkspaceRef => {
  resourceGroup: resourceGroupFromResourceId(monitoringWorkspaceResourceId)
  name: last(split(monitoringWorkspaceResourceId, '/'))
}

@export()
type eventgridNamespaceRef = {
  resourceGroup: resourceGroupReference
  name: string
}

@export()
func eventgridNamespaceRefFromId(eventgridNamespaceResourceId string) eventgridNamespaceRef => {
  resourceGroup: resourceGroupFromResourceId(eventgridNamespaceResourceId)
  name: last(split(eventgridNamespaceResourceId, '/'))
}

@export()
type grafanaRef = {
  resourceGroup: resourceGroupReference
  name: string
}

@export()
func grafanaRefFromId(grafanaResourceId string) grafanaRef => {
  resourceGroup: resourceGroupFromResourceId(grafanaResourceId)
  name: last(split(grafanaResourceId, '/'))
}
