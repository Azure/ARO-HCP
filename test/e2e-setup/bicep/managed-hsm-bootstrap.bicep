targetScope = 'resourceGroup'

@description('Globally unique Managed HSM name (3-24 alphanumeric characters or hyphens).')
param hsmName string

@description('Name of the HSM-backed RSA key to create.')
param keyName string = 'cmk'

@description('Azure region for all resources.')
param location string = resourceGroup().location

@description('Base64-encoded PEM certificate containing quorum member 1 public key.')
@secure()
param wrappingCertificate0Base64 string

@description('Base64-encoded PEM certificate containing quorum member 2 public key.')
@secure()
param wrappingCertificate1Base64 string

@description('Base64-encoded PEM certificate containing quorum member 3 public key.')
@secure()
param wrappingCertificate2Base64 string

@minValue(2)
@maxValue(3)
@description('Number of private wrapping keys required to recover the security domain.')
param securityDomainQuorum int = 2

@description('Additional Entra object IDs that should be initial Managed HSM administrators.')
param additionalAdminObjectIds array = []

@description('Entra object IDs that should be able to create and manage keys in the Managed HSM.')
param keyManagerObjectIds array = []

@description('Enable irreversible purge protection. Recommended for production; leave false for disposable testing.')
param enablePurgeProtection bool = false

@minValue(7)
@maxValue(90)
@description('Soft-delete retention period in days. Azure Managed HSM requires at least 7 days.')
param softDeleteRetentionInDays int = 7

@allowed([
  2048
  3072
  4096
])
@description('RSA key size.')
param keySize int = 3072

@description('Change this value only when the bootstrap deployment script must run again.')
param forceUpdateTag string = 'retry-3'

var storageAccountName = take('mhsmsd${uniqueString(subscription().subscriptionId, resourceGroup().id, hsmName)}', 24)
var securityDomainContainerName = 'security-domain'
var securityDomainBlobName = '${hsmName}-security-domain.json'
var storageBlobDataContributorRoleDefinitionId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'ba92f5b4-2d11-453d-a403-e96b0029c9fe'
)

resource bootstrapIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${hsmName}-bootstrap'
  location: location
}

resource managedHsm 'Microsoft.KeyVault/managedHSMs@2025-05-01' = {
  name: hsmName
  location: location
  sku: {
    family: 'B'
    name: 'Standard_B1'
  }
  properties: {
    createMode: 'default'
    enablePurgeProtection: enablePurgeProtection
    enableSoftDelete: true
    initialAdminObjectIds: concat([
      bootstrapIdentity.properties.principalId
    ], additionalAdminObjectIds)
    publicNetworkAccess: 'Enabled'
    softDeleteRetentionInDays: softDeleteRetentionInDays
    tenantId: tenant().tenantId
  }
}

resource securityDomainStorage 'Microsoft.Storage/storageAccounts@2025-01-01' = {
  name: storageAccountName
  location: location
  sku: {
    name: 'Standard_GRS'
  }
  kind: 'StorageV2'
  properties: {
    allowBlobPublicAccess: false
    allowSharedKeyAccess: false
    defaultToOAuthAuthentication: true
    minimumTlsVersion: 'TLS1_2'
    publicNetworkAccess: 'Enabled'
    supportsHttpsTrafficOnly: true
  }
}

resource blobService 'Microsoft.Storage/storageAccounts/blobServices@2025-01-01' = {
  parent: securityDomainStorage
  name: 'default'
  properties: {
    containerDeleteRetentionPolicy: {
      enabled: true
      days: 90
    }
    deleteRetentionPolicy: {
      enabled: true
      days: 90
    }
    isVersioningEnabled: true
  }
}

resource securityDomainContainer 'Microsoft.Storage/storageAccounts/blobServices/containers@2025-01-01' = {
  parent: blobService
  name: securityDomainContainerName
  properties: {
    publicAccess: 'None'
  }
}

resource securityDomainWriter 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: securityDomainStorage
  name: guid(securityDomainStorage.id, bootstrapIdentity.id, storageBlobDataContributorRoleDefinitionId)
  properties: {
    principalId: bootstrapIdentity.properties.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: storageBlobDataContributorRoleDefinitionId
  }
}

resource bootstrap 'Microsoft.Resources/deploymentScripts@2023-08-01' = {
  name: '${hsmName}-activate-and-create-key'
  location: location
  kind: 'AzureCLI'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${bootstrapIdentity.id}': {}
    }
  }
  properties: {
    azCliVersion: '2.59.0'
    cleanupPreference: 'OnSuccess'
    forceUpdateTag: forceUpdateTag
    retentionInterval: 'P1D'
    timeout: 'PT45M'
    environmentVariables: [
      {
        name: 'HSM_NAME'
        value: hsmName
      }
      {
        name: 'KEY_NAME'
        value: keyName
      }
      {
        name: 'KEY_SIZE'
        value: string(keySize)
      }
      {
        name: 'SD_QUORUM'
        value: string(securityDomainQuorum)
      }
      {
        name: 'STORAGE_ACCOUNT'
        value: securityDomainStorage.name
      }
      {
        name: 'STORAGE_CONTAINER'
        value: securityDomainContainer.name
      }
      {
        name: 'STORAGE_BLOB'
        value: securityDomainBlobName
      }
      {
        name: 'BOOTSTRAP_PRINCIPAL_ID'
        value: bootstrapIdentity.properties.principalId
      }
      {
        name: 'KEY_MANAGER_OBJECT_IDS_JSON'
        value: string(keyManagerObjectIds)
      }
      {
        name: 'SECURITY_DOMAIN_BLOB_URI'
        value: '${securityDomainStorage.properties.primaryEndpoints.blob}${securityDomainContainer.name}/${securityDomainBlobName}'
      }
      {
        name: 'WRAPPING_CERTIFICATE_0_BASE64'
        secureValue: wrappingCertificate0Base64
      }
      {
        name: 'WRAPPING_CERTIFICATE_1_BASE64'
        secureValue: wrappingCertificate1Base64
      }
      {
        name: 'WRAPPING_CERTIFICATE_2_BASE64'
        secureValue: wrappingCertificate2Base64
      }
    ]
    scriptContent: '''
      #!/usr/bin/env bash
      set -euo pipefail

      log() {
        level="$1"
        shift
        printf '%s [%s] phase=%s %s\n' \
          "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
          "$level" \
          "$current_phase" \
          "$*" >&2
      }

      current_phase='initialize'
      work_dir="$(mktemp -d)"
      cleanup() {
        exit_code=$?
        if [ "$exit_code" -ne 0 ]; then
          log 'ERROR' "deployment script failed with exit code $exit_code"
        fi
        rm -rf "$work_dir"
      }
      trap cleanup EXIT

      log 'INFO' "starting bootstrap for HSM $HSM_NAME and key $KEY_NAME"

      current_phase='decode-wrapping-certificates'
      printf '%s' "$WRAPPING_CERTIFICATE_0_BASE64" | base64 -d > "$work_dir/cert_0.cer"
      printf '%s' "$WRAPPING_CERTIFICATE_1_BASE64" | base64 -d > "$work_dir/cert_1.cer"
      printf '%s' "$WRAPPING_CERTIFICATE_2_BASE64" | base64 -d > "$work_dir/cert_2.cer"
      log 'INFO' 'decoded three wrapping certificates'

      current_phase='activate-managed-hsm'
      sd_file="$work_dir/security-domain.json"
      activation_succeeded=false
      activation_result=''

      for attempt in $(seq 1 20); do
        rm -f "$sd_file"

        set +e
        activation_result="$(az keyvault security-domain download \
          --hsm-name "$HSM_NAME" \
          --security-domain-file "$sd_file" \
          --sd-quorum "$SD_QUORUM" \
          --sd-wrapping-keys \
            "$work_dir/cert_0.cer" \
            "$work_dir/cert_1.cer" \
            "$work_dir/cert_2.cer" \
          --only-show-errors \
          --output json 2>&1)"
        activation_exit_code=$?
        set -e

        activation_status="$(
          printf '%s' "$activation_result" |
            jq -r 'if type == "object" then (.status // "Unknown") else "Unknown" end' 2>/dev/null ||
            printf 'Unknown'
        )"

        if [ "$activation_exit_code" -eq 0 ] &&
           [ "$activation_status" = 'Success' ] &&
           [ -s "$sd_file" ]; then
          activation_succeeded=true
          break
        fi

        log 'WARN' "attempt $attempt failed with status $activation_status"
        sleep 30
      done

      if [ "$activation_succeeded" != true ]; then
        log 'ERROR' 'activation did not produce a security-domain file'
        printf '%s\n' "$activation_result" >&2
        exit 1
      fi
      log 'INFO' 'Managed HSM activation completed successfully'

      current_phase='persist-security-domain'
      uploaded=false
      upload_result=''
      for attempt in $(seq 1 20); do
        set +e
        upload_result="$(az storage blob upload \
            --auth-mode login \
            --account-name "$STORAGE_ACCOUNT" \
            --container-name "$STORAGE_CONTAINER" \
            --name "$STORAGE_BLOB" \
            --file "$sd_file" \
            --overwrite true \
            --only-show-errors \
            --output json 2>&1)"
        upload_exit_code=$?
        set -e

        if [ "$upload_exit_code" -eq 0 ]; then
          uploaded=true
          break
        fi
        log 'WARN' "security-domain upload attempt $attempt failed"
        sleep 15
      done

      if [ "$uploaded" != true ]; then
        log 'ERROR' 'failed to persist the security-domain backup'
        printf '%s\n' "$upload_result" >&2
        exit 1
      fi
      log 'INFO' "security-domain backup persisted to $SECURITY_DOMAIN_BLOB_URI"

      current_phase='assign-managed-hsm-roles'
      ensure_crypto_user_role() {
        principal_id="$1"
        principal_type="${2:-}"

        for attempt in $(seq 1 20); do
          existing_assignments="$(az keyvault role assignment list \
            --hsm-name "$HSM_NAME" \
            --assignee-object-id "$principal_id" \
            --role 'Managed HSM Crypto User' \
            --scope '/' \
            --query 'length(@)' \
            --output tsv \
            --only-show-errors 2>/dev/null || printf '0')"

          if [ "$existing_assignments" != '0' ]; then
            return 0
          fi

          principal_type_arguments=()
          if [ -n "$principal_type" ]; then
            principal_type_arguments=(--assignee-principal-type "$principal_type")
          fi

          set +e
          role_assignment_result="$(az keyvault role assignment create \
              --hsm-name "$HSM_NAME" \
              --assignee-object-id "$principal_id" \
              "${principal_type_arguments[@]}" \
              --role 'Managed HSM Crypto User' \
              --scope '/' \
              --only-show-errors \
              --output json 2>&1)"
          role_assignment_exit_code=$?
          set -e

          if [ "$role_assignment_exit_code" -eq 0 ]; then
            log 'INFO' "assigned Managed HSM Crypto User to $principal_id"
            return 0
          fi

          log 'WARN' "role assignment attempt $attempt failed for $principal_id"
          sleep 15
        done

        log 'ERROR' "failed to assign Managed HSM Crypto User to $principal_id"
        printf '%s\n' "$role_assignment_result" >&2
        return 1
      }

      ensure_crypto_user_role "$BOOTSTRAP_PRINCIPAL_ID" 'MSI'

      while IFS= read -r principal_id; do
        if [ -n "$principal_id" ]; then
          ensure_crypto_user_role "$principal_id"
        fi
      done < <(printf '%s' "$KEY_MANAGER_OBJECT_IDS_JSON" | jq -r '.[]')

      current_phase='create-managed-hsm-key'
      key_id="$(az keyvault key show \
        --hsm-name "$HSM_NAME" \
        --name "$KEY_NAME" \
        --query key.kid \
        --output tsv \
        --only-show-errors 2>/dev/null || true)"

      if [ -z "$key_id" ]; then
        key_creation_result=''
        for attempt in $(seq 1 20); do
          set +e
          key_creation_result="$(az keyvault key create \
              --hsm-name "$HSM_NAME" \
              --name "$KEY_NAME" \
              --kty RSA-HSM \
              --size "$KEY_SIZE" \
              --ops encrypt decrypt \
              --only-show-errors \
              --output json 2>&1)"
          key_creation_exit_code=$?
          set -e

          if [ "$key_creation_exit_code" -eq 0 ]; then
            key_id="$(
              printf '%s' "$key_creation_result" |
                jq -r '.key.kid // empty' 2>/dev/null ||
                true
            )"
          fi

          if [ -n "$key_id" ]; then
            break
          fi

          log 'WARN' "key creation attempt $attempt failed"
          sleep 15
        done
      fi

      if [ -z "$key_id" ]; then
        log 'ERROR' 'Managed HSM was activated, but key creation failed'
        printf '%s\n' "$key_creation_result" >&2
        exit 1
      fi
      log 'INFO' "key is ready: $key_id"

      current_phase='write-outputs'
      jq -n -c \
        --arg keyId "$key_id" \
        --arg securityDomainBlobUri "$SECURITY_DOMAIN_BLOB_URI" \
        '{keyId: $keyId, securityDomainBlobUri: $securityDomainBlobUri}' \
        > "$AZ_SCRIPTS_OUTPUT_PATH"
      log 'INFO' 'bootstrap completed successfully'
    '''
  }
  dependsOn: [
    managedHsm
    securityDomainWriter
  ]
}

output managedHsmUri string = managedHsm.properties.hsmUri
output keyId string = bootstrap.properties.outputs.keyId
output securityDomainBlobUri string = bootstrap.properties.outputs.securityDomainBlobUri
output bootstrapIdentityPrincipalId string = bootstrapIdentity.properties.principalId
