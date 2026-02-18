# ACR Pull-Through Cache Priming

After deploying the updated `global-acr.bicep` with the new Velero cache rules, prime the cache by running:

## Prerequisites
- Azure CLI authenticated
- Correct subscription set
- ACRs deployed with updated global-acr.bicep

## For Each Environment

**Dev:**
```bash
az account set --subscription "Azure Red Hat OpenShift v4.x - Development"
./prime-cache.sh
```

**Int:**
```bash
az account set --subscription "Azure Red Hat OpenShift v4.x - HCP"
ACR_NAME=arohcpsvcint ./prime-cache.sh
```

**Stage:**
```bash
az account set --subscription "<stage-subscription>"
ACR_NAME=arohcpsvcstg ./prime-cache.sh
```

**Prod:**
```bash
az account set --subscription "<prod-subscription>"
ACR_NAME=arohcpsvcprod ./prime-cache.sh
```

## Verification

```bash
az acr repository list --name <acr-name> --output table | grep velero
```

Should show:
- `quay-cache/konveyor/velero`
- `quay-cache/konveyor/velero-plugin-for-microsoft-azure`
