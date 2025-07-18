param azureMonitoring string

resource clusterServiceRecordingRules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'cluster-service-recording-rules'
  location: resourceGroup().location
  properties: {
    description: 'Recording rules for cluster service SLO calculations'
    scopes: [
      azureMonitoring
    ]
    enabled: true
    interval: 'PT1M'
    rules: [
      {
        record: 'availability:api_inbound_request_count:burnrate5m'
        expression: '( sum(max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..", service="clusters-service-metrics"}[5m])))  / sum(max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[5m]))) )'
      }
      {
        record: 'availability:api_inbound_request_count:burnrate1h'
        expression: '( sum(max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..", service="clusters-service-metrics"}[1h])))  / sum(max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[1h]))) )'
      }
      {
        record: 'availability:api_inbound_request_count:burnrate30m'
        expression: '( sum(max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..", service="clusters-service-metrics"}[30m])))  / sum(max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[30m]))) )'
      }
      {
        record: 'availability:api_inbound_request_count:burnrate6h'
        expression: '( sum(max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service",code=~"5..", service="clusters-service-metrics"}[6h])))  / sum(max without(prometheus_replica) (rate(api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics"}[6h]))) )'
      }
    ]
  }
} 
