# copy from maestro/Makefile#L14
deploy-server:
    TENANT_ID="{{index . "tenantId"}}"
    REGION_RG="{{index . "region_resourcegroup"}}"
    EVENTGRID_NS="{{index . "region_eventgrid_namespace"}}"
    MAESTRO_KV="{{index . "region_maestro_keyvault"}}"
    SERVICE_RG="{{index . "svc_resourcegroup"}}"
    AKS="{{index . "aks_name"}}"
    MAESTRO_MI="{{index . "maestro_msi"}}"
    HELM_CHART="{{index . "maestro_helm_chart"}}"

    EVENTGRID_HOSTNAME=$(az event namespace show -g "${REGION_RG}" -n "${EVENTGRID_NS}" --query "properties.topicSpacesConfiguration.hostname")
    MAESTRO_MI_CLIENT_ID=$(az identity show -g "${SERVICE_RG}" -n "${MAESTRO_MI}" --query "clientId")
    ISTO_VERSION=$(az aks show -g "${SERVICE_RG}" -n "${AKS}" --query "serviceMeshProfile.istio.revisions[-1]")

    kubectl create namespace maestro --dry-run=client -o json | kubectl apply -f - && \
    kubectl label namespace maestro "istio.io/rev=${ISTO_VERSION}" --overwrite=true && \
    helm upgrade --install maestro-server "${HELM_CHART}" \
        --namespace maestro \
        --set broker.host=${EVENTGRID_HOSTNAME} \
        --set credsKeyVault.name=${MAESTRO_KV} \
        --set azure.clientId=${MAESTRO_MI_CLIENT_ID} \
        --set azure.tenantId=${TENANT_ID} \
        --set image.base='quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro'\
        --set database.containerizedDb=true \
        --set database.ssl=disable