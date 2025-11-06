using '../templates/pko-lookup.bicep'

param pkoMsiName = '{{ .pko.managedIdentityName }}'
param imagePullerMsiName = 'image-puller'
