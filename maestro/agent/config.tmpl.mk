EVENTGRID_NAME ?= {{ .maestro.eventGrid.name }}
REGION_RG ?= {{ .regionRG }}
MGMT_RG ?= {{ .mgmt.rg }}
CONSUMER_NAME ?= {{ .maestro.consumerName }}
KEYVAULT_NAME ?= {{ .maestro.keyVaultName }}
IMAGE_BASE ?= {{ .maestro.imageBase }}
IMAGE_TAG ?= {{ .maestro.imageTag }}
