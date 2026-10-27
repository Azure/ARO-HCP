{{/*
Renders the subscription entries for a ci.<env> CSV of "SubscriptionID=DisplayName"
pairs, each monitored in every region of the CSV regions list.
Expects a dict with "subscriptions" and "regions" CSV strings.
*/}}
{{- define "tenant-quota.subscriptions" -}}
{{- $regions := splitList "," .regions -}}
{{- if .subscriptions -}}
{{- range $sub := splitList "," .subscriptions }}
- name: {{ last (splitList "=" $sub) | quote }}
  regions:
  {{- range $regions }}
  - {{ . | quote }}
  {{- end }}
{{- end }}
{{- end }}
{{- end }}
