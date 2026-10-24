{{/* Count rule matches before each rewrite, separating KEEP=false reroutes from KEEP=true copies.
Compare these counters with fluentbit_filter_emit_records_total for the router.
The pod-name Shoebox rule excludes matches of its preceding container-name rule.
*/}}
{{- define "arobit.routingMetrics" -}}
[FILTER]
    Name               log_to_metrics
    Alias              filter.metrics_{{ .type }}_{{ .name }}
    Match              {{ .match }}
{{- with .exclude }}
    Exclude            {{ . }}
{{- end }}
    Regex              {{ .regex }}
    Metric_Mode        counter
    Metric_Namespace   arobit
    Metric_Subsystem   routing_{{ .type }}
    Metric_Name        {{ .name }}_matched_total
    Metric_Description Records matching {{ .type }} rules in {{ .name }}
    Tag                metrics.fluentbit
    Emitter_Name       metrics_{{ .type }}_{{ .name }}
    Flush_Interval_Sec 5
{{- end -}}
