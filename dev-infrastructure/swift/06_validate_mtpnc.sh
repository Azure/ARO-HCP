set -o errexit
set -o nounset
set -o pipefail

source swift_env_vars

kubectl get mtpnc nginx-swift-test -o yaml