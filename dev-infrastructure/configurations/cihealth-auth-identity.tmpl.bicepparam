using '../templates/cihealth-auth-identity.bicep'

param applicationName = '{{ .opstool.cihealthAuth.applicationName }}'
param applicationUniqueName = '{{ .opstool.cihealthAuth.applicationUniqueName }}'
param redirectUri = 'https://{{ .opstool.cihealthAuth.httpRoute.host }}/oauth2/callback'
