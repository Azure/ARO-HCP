EVENTGRID_NAME ?= {{ .maestroEventgridName}}
REGION_RG ?= {{ .regionRG }}
MGMT_RG ?= {{ .managementClusterRG }}
CONSUMER_NAME ?= {{ .maestroConsumerName }}
KEYVAULT_NAME ?= {{ .maestroKeyVaultName }}
IMAGE_BASE ?= {{ .maestroImageBase }}
IMAGE_TAG ?= {{ .maestroImageTag }}
