
show_info_box() {
  gum style --border double --margin "1" --padding "1 2" --border-foreground 99 \
  "📘 External Entra Auth Prerequisites:" "" \
  "• A fully created ARO-HCP cluster" \
  "• A Hosted Control Plane (HCP) running" \
  "• A NodePool created and provisioned" \
  "• RP is port-forwarded and reachable on localhost:8443" \
  "• Azure CLI is authenticated and target subscription is selected"
  echo ""
}

#!/usr/bin/env bash
set -euo pipefail

# Icons
K8S="☸️"
AZ="🔷"
GIT="🌳"
CHECK="✅"
PENDING="⏳"

# Dependencies check
install_if_missing() {
  cmd=$1
  pkg=$2
  icon=$3
  if ! command -v "$cmd" &>/dev/null; then
    echo "$icon Installing $cmd..."
    sudo dnf install -y "$pkg"
  fi
}

check_deps() {
  install_if_missing az azure-cli "$AZ"
  install_if_missing kubectl kubectl "$K8S"
  install_if_missing jq jq "🧩"
  install_if_missing gum gum "💎"
  install_if_missing git git "$GIT"
}

# Verify az login
verify_az_login() {
  echo "$AZ Verifying Azure login..."
  az account show &>/dev/null || az login
}

# Print step with checkboxes
declare -A STATUS
STATUS=(
  [entra]="⏳"
  [group]="⏳"
  [callback]="⏳"
  [update_uri]="⏳"
  [apply_rp]="⏳"
  [test]="⏳"
)

print_status() {
  clear
  echo "🔐 External Auth Entra Setup Progress:"
  echo "${STATUS[entra]} Create Entra App"
  echo "${STATUS[group]} Create AD Group & Add User"
  echo "${STATUS[callback]} Get Cluster Callback URL"
  echo "${STATUS[update_uri]} Update Entra Redirect URI"
  echo "${STATUS[apply_rp]} Apply Config via RP"
  echo "${STATUS[test]} Test Redirect"
  echo ""
}

create_entra_app() {
  print_status
  echo "$AZ Creating Entra App..."
  app_name="ARO-HCP-Auth-$(date +%s)"
  app_info=$(az ad app create --display-name "$app_name" --query '{appId: appId, id: id}' -o json)
  client_id=$(echo "$app_info" | jq -r '.appId')
  app_obj_id=$(echo "$app_info" | jq -r '.id')
  secret_info=$(az ad app credential reset --id "$client_id" --append --display-name "AROSecret" -o json)
  client_secret=$(echo "$secret_info" | jq -r '.password')
  echo "client_id=$client_id" > demo_env.sh
  echo "client_secret=$client_secret" >> demo_env.sh
  echo "app_obj_id=$app_obj_id" >> demo_env.sh
  echo "app_name=$app_name" >> demo_env.sh
  STATUS[entra]="✅"
}

create_ad_group() {
  print_status
  echo "👥 Creating AD Group..."
  source demo_env.sh
  group_name="ARO-HCP-Admins"
  az ad group create --display-name "$group_name" --mail-nickname "$group_name" &>/dev/null || true
  read -p "Enter user email to add: " user_email
  user_id=$(az ad user show --id "$user_email" --query id -o tsv)
  az ad group member add --group "$group_name" --member-id "$user_id"
  STATUS[group]="✅"
}

get_callback_url() {
  print_status
  echo "$K8S Fetching callback URL..."
  echo ""
  echo "🔐 Ensure you're authenticated to the correct cluster (management or hosted control plane)."
  echo "If you encounter x509 certificate errors:"
  echo "   - Run ./request-admin-credential.sh to create break-glass credentials"
  echo "   - export KUBECONFIG=./kubeconfig"
  echo "   - Use --insecure-skip-tls-verify for kubectl commands"
  echo ""

  hcp_ns=$(gum input --placeholder "Enter Hypershift namespace:")
  hcp_name=$(gum input --placeholder "Enter Hypershift cluster name:")

  echo ""
  echo "🔍 Attempting to retrieve callback URL from HostedCluster..."
  callback_url=$(kubectl get hostedcluster "$hcp_name" -n "$hcp_ns" -o jsonpath="{.status.oauthCallbackURL}" 2>/dev/null || echo "")

  if [[ -z "$callback_url" ]]; then
    echo "HostedCluster callback URL not available. Attempting to get OpenShift console route..."
    callback_url=$(kubectl get route console -n openshift-console --insecure-skip-tls-verify -o jsonpath="{.spec.host}" 2>/dev/null || echo "")
    
    if [[ -z "$callback_url" ]]; then
      echo "⚠️ Failed to retrieve callback URL from both HostedCluster and OpenShift console route."
      echo "↩️ Returning to menu without setting callback URL."
      return
    else
      echo "✅ Found fallback callback URL from OpenShift console route: https://$callback_url"
      callback_url="https://$callback_url"
    fi
  else
    echo "✅ Callback URL from HostedCluster: $callback_url"
  fi

  echo "callback_url=$callback_url" >> demo_env.sh
  STATUS[callback]="✅"
}


update_app_redirect_uri() {
  print_status
  echo "$AZ Updating redirect URI..."
  source demo_env.sh

  # Ensure the callback URL is present
  if [[ -z "${callback_url:-}" ]]; then
    echo "❌ callback_url is not set. Please run 'Get Callback URL' step first."
    return
  fi

  # Ensure it ends with /oauth/callback
  redirect_uri="${callback_url%/}/oauth/callback"
  echo "🔗 Setting redirect URI to: $redirect_uri"

  az ad app update --id "$client_id" --web-redirect-uris "$redirect_uri"

  STATUS[update_uri]="✅"
}

apply_idp_config_via_rp() {
  print_status
  echo "📡 Preparing to send external auth config to RP frontend..."

  echo ""
  echo "💡 Ensure RP is forwarded:"
  echo "   kubectl port-forward svc/aro-hcp-frontend -n aro-hcp 8443:8443"
  echo ""

  source demo_env.sh

  # Check if IDP is already configured
  echo "🔍 Checking existing IDPs in the HostedCluster..."
  if kubectl get authentication cluster --insecure-skip-tls-verify -o json | jq -e '.spec.identityProviders | length > 0' >/dev/null; then
    echo "⚠️ Identity Provider already configured in the cluster."
    gum confirm "Return to menu?" && return
  else
    echo "✅ No existing IDP found."
  fi

  # Get Entra app name or ID
  read -p "Enter the external auth ID (e.g., entra): " external_auth_id

  # Get access token
  ACCESS_TOKEN=$(az account get-access-token --query accessToken -o tsv 2>/dev/null || true)
  if [[ -z "$ACCESS_TOKEN" ]]; then
    read -p "Could not auto-acquire access token. Please enter it manually: " ACCESS_TOKEN
  else
    echo "🔑 Azure access token acquired."
  fi

  # Get subscription/RG/cluster name
  default_sub=$(az account show --query id -o tsv 2>/dev/null || echo "")
  read -p "Enter your subscription ID [default: $default_sub]: " subscription_id
  subscription_id=${subscription_id:-$default_sub}
  read -p "Enter your resource group name: " resource_group
  read -p "Enter your cluster name: " cluster_name

  # Build RP URL
  rp_url="http://localhost:8443/subscriptions/$subscription_id/resourceGroups/$resource_group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/$cluster_name/externalAuths/$external_auth_id?api-version=2024-06-10-preview"
  created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  echo ""
  echo "🔗 RP Endpoint: $rp_url"
  echo "🚀 Sending PUT request with payload..."

  # Execute request
  curl -s -w "%{http_code}" --fail-with-body -o rp_response.log -X PUT "$rp_url" \
    -H "Authorization: Bearer $ACCESS_TOKEN" \
    -H "Content-Type: application/json" \
    -H "X-Ms-Identity-Url: https://dummy.identity.azure.net" \
    -H "X-Ms-Arm-Resource-System-Data: {\"createdBy\": \"dev-user\", \"createdByType\": \"User\", \"createdAt\": \"$created_at\"}" \
    --data-binary @external-auth-payload.json || echo "error" >> rp_response.log

  echo ""
  echo "Logging RP pod logs (top 30 lines)..."

  echo "Switching to Service Cluster for logs..."
  export KUBECONFIG=$(make infra.svc.aks.kubeconfigfile 2>/dev/null)

  if [[ -z "$KUBECONFIG" ]]; then
    echo "Could not switch to service cluster. Skipping pod log capture." >> rp_response.log
  else
    echo "Capturing logs from RP frontend pod..."
    {
      echo ""
      echo "================ RP FRONTEND POD LOGS (top 30) ================"
      kubectl logs deployment/aro-hcp-frontend -c aro-hcp-frontend -n aro-hcp --tail=30 2>&1 || echo "Failed to get logs"
    } >> rp_response.log
  fi

  echo ""
  if grep -q '"status": *"Succeeded"' rp_response.log; then
    echo "✅ Successfully applied external auth config to RP."
    STATUS[apply_rp]="✅"
  else
    echo "❌ Failed to apply config or confirm success."
    echo "📄 See full logs in rp_response.log"
    gum confirm "Retry apply to RP?" && apply_idp_config_via_rp || echo "↩️ Returning to main menu."
  fi
}


test_login_redirect() {
  print_status
  source demo_env.sh
  gum confirm "Open callback URL in browser?" && xdg-open "$callback_url"
  STATUS[test]="✅"
}

run_flow() {
  check_deps
  verify_az_login
  create_entra_app
  create_ad_group
  get_callback_url
  update_app_redirect_uri
  apply_idp_config_via_rp
  test_login_redirect
  print_status
  echo "🎉 All tasks completed."
}

run_flow
