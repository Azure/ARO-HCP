targetScope = 'subscription'

// Applies inherited subscription and resource-group tags to Cost Management
// usage records, without overriding tags set directly on a resource.
resource tagInheritance 'Microsoft.CostManagement/settings@2025-03-01' = {
  name: 'taginheritance'
  kind: 'taginheritance'
  properties: {
    preferContainerTags: false
  }
}
