// TRANSIENT: STG-global "V2" copy of kusto-lookup.tmpl.bicepparam. Identical to the
// canonical file except the (globally-unique) Kusto cluster name is sourced from the
// transient stgGlobalV2 block, so the lookup resolves the same cluster that
// kusto-stg.tmpl.bicepparam deploys instead of the live V1 cluster.
// Remove together with kusto-stg.tmpl.bicepparam and geography-pipeline-stg.yaml once
// stgGlobalV2 is retired and stg uksouth is served solely by the canonical pipeline.
using '../templates/kusto-lookup.bicep'

param kustoName = '{{ .stgGlobalV2.kustoName }}'

param kustoEnabled = {{ .arobit.kusto.enabled }}
