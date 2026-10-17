using '../templates/fleet-alert-processing-rule-assignment.bicep'

// Fleet identity
// used to grant Monitoring Contributor access for alert processing rule cleanup
param fleetPrincipalId = '__fleetPrincipalId__'
