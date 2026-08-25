SVC_CLUSTER ?= {{ .svc.aks.name }}
REGION ?= {{ .region }}
REGION_RG ?= {{ .regionRG }}
HCP_WORKSPACE_NAME ?= {{ .monitoring.hcpWorkspaceName }}
KUSTO_NAME ?= {{ .kusto.kustoName }}
KUSTO_REGION ?= {{ .kusto.location }}