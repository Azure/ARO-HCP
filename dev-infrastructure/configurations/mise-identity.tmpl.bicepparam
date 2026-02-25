using '../templates/mise-identity.bicep'

param miseApplicationName = '{{ .mise.applicationName }}'
param miseApplicationOwnerIds = '{{ .geneva.actions.application.ownerIds }}'
param miseApplicationDeploy = {{ .mise.deploy }}
