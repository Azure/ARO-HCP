#!/bin/bash
set -euo pipefail

# Run after bundle extraction or policy refresh. Fail rather than silently lose
# resource controls when the upstream deployment structure changes.
source_dir=$(dirname "$(realpath "$0")")/resource-templates
chart=$2
case "$1" in
  mce)
    cp "$source_dir/_resources.tpl" "$chart/templates/_resources.tpl"
    yq eval-all -i 'select(fileIndex == 0) * select(fileIndex == 1)' "$chart/values.yaml" "$source_dir/mce-values.yaml"
    RESOURCE_KEY=backplaneOperator perl -0pi -e '
      $replacement = qq{        resources:\n          {{- include "acm.resources" .Values.resources.$ENV{RESOURCE_KEY} | nindent 10 }}\n};
      if (index($_, $replacement) < 0) {
        s/^        resources:\n(?:^          .*\n)+/$replacement/mg == 1 or die "Expected one operator resources block in $ARGV\n";
      }
    ' "$chart/templates/multicluster-engine-operator.deployment.yaml"
    perl -0pi -e '
      if (!/include "acm.resources"/) {
        s/^(        name: finalize\n)/$1        resources:\n          {{- include "acm.resources" .Values.resources.finalize | nindent 10 }}\n/m == 1
          or die "Expected one finalize container in $ARGV\n";
      }
    ' "$chart/templates/finalize-mce.job.yaml"
    ;;
  policy)
    mkdir -p "$chart/templates"
    cp "$source_dir/_resources.tpl" "$chart/templates/_resources.tpl"
    for entry in \
      grc:grc-policy-addon-controller.yaml:policyAddonController \
      grc:grc-policy-propagator.yaml:policyPropagator \
      cluster-lifecycle:klusterlet-addon-deployment.yaml:klusterletAddonController; do
      IFS=: read -r subchart template key <<< "$entry"
      RESOURCE_KEY=$key perl -0pi -e '
        $replacement = qq{        resources:\n          {{- include "acm.resources" .Values.resources.$ENV{RESOURCE_KEY} | nindent 10 }}\n};
        if (index($_, $replacement) < 0) {
          s/^        resources:\n(?:^          .*\n)+/$replacement/mg == 1 or die "Expected one policy resources block in $ARGV\n";
        }
      ' "$chart/charts/$subchart/templates/$template"
    done
    ;;
  *) exit 1 ;;
esac
