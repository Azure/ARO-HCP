using '../templates/fleet-alert-processing-rule-assignment.bicep'

// Fleet managed identity
// used to grant Monitoring Contributor access for alert processing rule cleanup
param fleetMIName = '{{ .fleet.managedIdentityName }}'
param fleetMIResourceGroup = '{{ .svc.rg }}'
