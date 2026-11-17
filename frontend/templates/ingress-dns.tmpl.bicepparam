using './ingress-dns.bicep'

param hostname = '{{ .frontend.cert.san }}'
param activate = {{ .svc.rpIngress.activate }}
param publicIPAddressName = '{{ .svc.rpIngress.ipAddressName }}'
