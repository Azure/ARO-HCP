using 'template.bicep'

param items = [{{ range .items }}
  '{{ . }}'
{{ end }}]
