/*
 * Registers AFEC feature flags.  This does not wait for registration to complete.
 */

targetScope = 'subscription'

@description('The namespace and name of an AFEC feature flag in a `Microsoft.ResourceProvider/FeatureName` format')
param features array = [
  'Microsoft.ContainerService/DisableSSHPreview'
  'Microsoft.ContainerService/IstioNativeSidecarModePreview'
  'Microsoft.Compute/EncryptionAtHost'
  'Microsoft.Network/AllowBringYourOwnPublicIpAddress'
]

resource featureReg 'Microsoft.Features/featureProviders/subscriptionFeatureRegistrations@2021-07-01' = [
  for feature in features: {
    name: feature
  }
]
