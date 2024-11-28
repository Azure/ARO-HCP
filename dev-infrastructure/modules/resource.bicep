// bicep func to extract subscription, resourcegroup from a resource id

@export()
type resourceGroupReference = {
  subscriptionId: string
  name: string
}

@export()
type msiRef = {
  resourceGroup: resourceGroupReference
  name: string
}

@export()
func resourceGroupFromResourceId(resourceId string) resourceGroupReference => {
  subscriptionId: split(resourceId, '/')[2]
  name: split(resourceId, '/')[4]
}

@export()
func msiRefFromId(msiResourceId string) msiRef => {
  resourceGroup: resourceGroupFromResourceId(msiResourceId)
  name: last(split(msiResourceId, '/'))
}
