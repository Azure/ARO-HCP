using '../templates/arobit-lookup.bicep'

param msiName = '{{ .logs.mdsd.msiName }}'
param infrastructureIdentityResourceGroup = {{ .infrastructureIdentities.useLeased }} ? '{{ .infrastructureIdentities.serviceResourceGroup }}' : '{{ .svc.rg }}'
