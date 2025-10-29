@description('Name of the Kusto cluster owning this database')
param kustoName string

resource kustoQoS 'Microsoft.Insights/actionGroups@2022-06-01' = {
  name: 'KustoQoSAction'
  location: 'global'
  properties: {
    groupShortName: 'KustoHealth'
    enabled: true
    emailReceivers: [
      {
        name: 'EmailAROTeam_-EmailAction-'
        emailAddress: 'aromsfteng@microsoft.com'
        useCommonAlertSchema: false
      }
    ]
  }
}

resource kustoQoSAlert 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: 'KustoQosAlert'
  location: 'global'
  properties: {
    description: 'Alert when Kusto keep alive metric falls below 50%'
    severity: 4
    enabled: true
    scopes: [
      kustoName
    ]
    evaluationFrequency: 'PT30M'
    windowSize: 'PT1H'
    criteria: {
      allOf: [
        {
          threshold: 1 / 2
          name: 'Metric1'
          metricNamespace: 'Microsoft.Kusto/clusters'
          metricName: 'KeepAlive'
          operator: 'LessThanOrEqual'
          timeAggregation: 'Average'
          criterionType: 'StaticThresholdCriterion'
        }
      ]
      'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
    }
    autoMitigate: true
    targetResourceType: 'Microsoft.Kusto/clusters'
    targetResourceRegion: resourceGroup().location
    actions: [
      {
        actionGroupId: kustoQoS.id
      }
    ]
  }
}
