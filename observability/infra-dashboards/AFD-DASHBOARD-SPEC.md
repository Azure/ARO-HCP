# Azure Front Door Enhanced Observability Dashboard

This document describes the enhanced AFD dashboard that combines Azure Monitor metrics with Log Analytics detailed logs.

## Dashboard Overview

**Title**: Azure Front Door - Enhanced Observability  
**UID**: `afd-enhanced-observability`  
**Tags**: azure, front-door, cdn, networking, observability  
**Refresh**: 30s  

## Variables

1. **prometheus_datasource** (Data Source)
   - Type: Datasource
   - Query: `prometheus`
   - Regex: `/^Managed_Prometheus_services-.*/`
   - Label: "Prometheus Data Source"

2. **loganalytics_datasource** (Data Source)
   - Type: Datasource
   - Query: `grafana-azure-monitor-datasource`
   - Label: "Log Analytics Data Source"

3. **frontdoor_profile** (Query)
   - Datasource: `${prometheus_datasource}`
   - Query: `label_values(azure_microsoft_cdn_profiles_requestcount_total, _azure_resource_name)`
   - Label: "Front Door Profile"

## Panels

### Row 1: Overview - Real-time Metrics (Azure Monitor)

#### Panel 1.1: Request Rate (Stat)
- **Query**: `sum(rate(azure_microsoft_cdn_profiles_requestcount_total{_azure_resource_name=~"$frontdoor_profile"}[5m]))`
- **Unit**: req/s
- **Description**: Total requests per second across all endpoints

#### Panel 1.2: Avg Total Latency (Stat)
- **Query**: `avg(azure_microsoft_cdn_profiles_totallatency_average{_azure_resource_name=~"$frontdoor_profile"})`
- **Unit**: ms
- **Thresholds**: Green (0), Yellow (500), Red (1000)
- **Description**: Average end-to-end latency including CDN processing

#### Panel 1.3: 4xx Error Rate (Stat)
- **Query**: `avg(azure_microsoft_cdn_profiles_percentage4xx_average{_azure_resource_name=~"$frontdoor_profile"})`
- **Unit**: percent
- **Thresholds**: Green (0), Yellow (5), Red (10)
- **Description**: Percentage of client errors

#### Panel 1.4: 5xx Error Rate (Stat)
- **Query**: `avg(azure_microsoft_cdn_profiles_percentage5xx_average{_azure_resource_name=~"$frontdoor_profile"})`
- **Unit**: percent
- **Thresholds**: Green (0), Yellow (1), Red (5)
- **Description**: Percentage of server errors

### Row 2: Traffic Patterns

#### Panel 2.1: Request Count by HTTP Status (Time Series)
- **Query**: `sum by (HttpStatusGroup) (rate(azure_microsoft_cdn_profiles_requestcount_total{_azure_resource_name=~"$frontdoor_profile"}[5m]))`
- **Legend**: `{{HttpStatusGroup}}`
- **Description**: Request volume broken down by HTTP status code groups

#### Panel 2.2: Request Count by Client Country (Time Series)
- **Query**: `topk(10, sum by (ClientCountry) (rate(azure_microsoft_cdn_profiles_requestcount_total{_azure_resource_name=~"$frontdoor_profile"}[5m])))`
- **Legend**: `{{ClientCountry}}`
- **Description**: Geographic distribution of requests (top 10 countries)

### Row 3: Latency Analysis

#### Panel 3.1: Total Latency by Client Country (Time Series)
- **Query**: `avg by (ClientCountry) (azure_microsoft_cdn_profiles_totallatency_average{_azure_resource_name=~"$frontdoor_profile"})`
- **Legend**: `{{ClientCountry}}`
- **Unit**: ms
- **Description**: End-to-end latency by geographic region

#### Panel 3.2: Origin Latency by Backend (Time Series)
- **Query**: `avg by (Origin) (azure_microsoft_cdn_profiles_originlatency_average{_azure_resource_name=~"$frontdoor_profile"})`
- **Legend**: `{{Origin}}`
- **Unit**: ms
- **Description**: Backend response time by origin server

### Row 4: Web Application Firewall (WAF)

#### Panel 4.1: WAF Actions (Time Series)
- **Query**: `sum by (Action) (rate(azure_microsoft_cdn_profiles_webapplicationfirewallrequestcount_total{_azure_resource_name=~"$frontdoor_profile"}[5m]))`
- **Legend**: `{{Action}}`
- **Description**: WAF rule actions (Block, Allow, Log)

#### Panel 4.2: Top WAF Rules Triggered (Time Series)
- **Query**: `topk(10, sum by (RuleName) (rate(azure_microsoft_cdn_profiles_webapplicationfirewallrequestcount_total{_azure_resource_name=~"$frontdoor_profile"}[5m])))`
- **Legend**: `{{RuleName}}`
- **Description**: Most frequently triggered WAF rules

### Row 5: Detailed Logs - Access Patterns (Log Analytics)

#### Panel 5.1: Top Requested URLs (Table)
- **Datasource**: `${loganalytics_datasource}`
- **Query Type**: Logs (KQL)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| where TimeGenerated > ago(6h)
| summarize RequestCount = count(), AvgDuration = avg(timeTaken_d) by requestUri_s
| top 20 by RequestCount desc
| project URL = requestUri_s, Requests = RequestCount, AvgDuration_ms = AvgDuration
```
- **Description**: Most frequently accessed URLs with average duration

#### Panel 5.2: Request Status Code Distribution (Pie Chart)
- **Datasource**: `${loganalytics_datasource}`
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| where TimeGenerated > $__timeFrom and TimeGenerated < $__timeTo
| summarize count() by httpStatusCode_d
| project StatusCode = tostring(httpStatusCode_d), Count = count_
```
- **Description**: Distribution of HTTP status codes

#### Panel 5.3: Requests Over Time by Status Code (Time Series)
- **Datasource**: `${loganalytics_datasource}`
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| where TimeGenerated > $__timeFrom and TimeGenerated < $__timeTo
| summarize count() by bin(TimeGenerated, $__interval), httpStatusCode_d
| project TimeGenerated, StatusCode = tostring(httpStatusCode_d), Count = count_
```
- **Description**: Request volume over time broken down by status code

### Row 6: Detailed Logs - Client Analysis (Log Analytics)

#### Panel 6.1: Top Client IPs (Table)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| where TimeGenerated > ago(6h)
| summarize Requests = count(), UniqueURLs = dcount(requestUri_s) by clientIp_s
| top 20 by Requests desc
| project ClientIP = clientIp_s, Requests, UniqueURLs
```
- **Description**: Most active client IP addresses

#### Panel 6.2: User Agent Distribution (Bar Chart)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| where TimeGenerated > ago(6h)
| extend UserAgentType = case(
    userAgent_s contains "bot" or userAgent_s contains "Bot", "Bot",
    userAgent_s contains "Mobile" or userAgent_s contains "Android" or userAgent_s contains "iPhone", "Mobile",
    userAgent_s contains "curl" or userAgent_s contains "wget", "CLI Tool",
    "Browser"
)
| summarize count() by UserAgentType
| project UserAgentType, Count = count_
```
- **Description**: Request distribution by user agent type

#### Panel 6.3: Geographic Request Map (Geomap)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| where TimeGenerated > $__timeFrom and TimeGenerated < $__timeTo
| summarize Requests = count() by clientCountry_s
| project Country = clientCountry_s, Requests
```
- **Description**: Geographic distribution of requests on world map

### Row 7: Performance Insights (Log Analytics)

#### Panel 7.1: Latency Percentiles (Time Series)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| where TimeGenerated > $__timeFrom and TimeGenerated < $__timeTo
| summarize 
    p50 = percentile(timeTaken_d, 50),
    p95 = percentile(timeTaken_d, 95),
    p99 = percentile(timeTaken_d, 99)
    by bin(TimeGenerated, $__interval)
| project TimeGenerated, p50, p95, p99
```
- **Description**: Request latency at different percentiles (p50, p95, p99)

#### Panel 7.2: Cache Hit Ratio (Time Series)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| where TimeGenerated > $__timeFrom and TimeGenerated < $__timeTo
| extend CacheStatus = case(
    cacheStatus_s == "HIT", "Hit",
    cacheStatus_s == "MISS", "Miss",
    "Other"
)
| summarize count() by bin(TimeGenerated, $__interval), CacheStatus
| project TimeGenerated, CacheStatus, Count = count_
```
- **Description**: Cache hit vs miss ratio over time

#### Panel 7.3: Slow Requests (Table)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorAccessLog"
| where TimeGenerated > ago(1h)
| where timeTaken_d > 1000  // Requests taking more than 1 second
| project 
    Time = TimeGenerated,
    URL = requestUri_s,
    Duration_ms = timeTaken_d,
    StatusCode = httpStatusCode_d,
    ClientIP = clientIp_s,
    Country = clientCountry_s
| top 50 by Duration_ms desc
```
- **Description**: Slowest requests in the last hour

### Row 8: Security - WAF Detailed Analysis (Log Analytics)

#### Panel 8.1: WAF Blocks Over Time (Time Series)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorWebApplicationFirewallLog"
| where TimeGenerated > $__timeFrom and TimeGenerated < $__timeTo
| where action_s == "Block"
| summarize count() by bin(TimeGenerated, $__interval)
| project TimeGenerated, BlockedRequests = count_
```
- **Description**: WAF blocked requests over time

#### Panel 8.2: Top Blocked IPs (Table)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorWebApplicationFirewallLog"
| where TimeGenerated > ago(6h)
| where action_s == "Block"
| summarize BlockCount = count(), Rules = make_set(ruleName_s) by clientIP_s
| top 20 by BlockCount desc
| project ClientIP = clientIP_s, BlockCount, TriggeredRules = Rules
```
- **Description**: IP addresses with most WAF blocks

#### Panel 8.3: WAF Rule Matches (Bar Chart)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorWebApplicationFirewallLog"
| where TimeGenerated > ago(6h)
| summarize count() by ruleName_s, action_s
| project Rule = ruleName_s, Action = action_s, Count = count_
| top 20 by Count desc
```
- **Description**: Most frequently matched WAF rules and their actions

### Row 9: Health & Availability (Log Analytics)

#### Panel 9.1: Backend Health Status (Time Series)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorHealthProbeLog"
| where TimeGenerated > $__timeFrom and TimeGenerated < $__timeTo
| extend HealthStatus = case(
    httpStatusCode_d >= 200 and httpStatusCode_d < 300, "Healthy",
    "Unhealthy"
)
| summarize count() by bin(TimeGenerated, $__interval), healthProbeId_s, HealthStatus
| project TimeGenerated, Backend = healthProbeId_s, HealthStatus, Count = count_
```
- **Description**: Backend health probe results over time

#### Panel 9.2: Failed Health Probes (Table)
- **Query**:
```kusto
AzureDiagnostics
| where Category == "FrontDoorHealthProbeLog"
| where TimeGenerated > ago(1h)
| where httpStatusCode_d < 200 or httpStatusCode_d >= 300
| project 
    Time = TimeGenerated,
    Backend = healthProbeId_s,
    StatusCode = httpStatusCode_d,
    Duration_ms = timeTaken_d
| top 50 by Time desc
```
- **Description**: Recent failed health probes

## How to Create This Dashboard

### Option 1: Build in Grafana UI
1. Open Grafana: https://arohcp-dev-c9g7a4fjanb0c4gc.wus3.grafana.azure.com
2. Create new dashboard
3. Add variables as described above
4. Add panels row by row using the queries provided
5. Export JSON when complete

### Option 2: Modify Existing Dashboard
1. Copy `observability/infra-dashboards/azure-front-door.json`
2. Add Log Analytics data source variable
3. Add new rows and panels for Log Analytics queries
4. Update title and UID

### Option 3: Use Dashboard Generator Script
A Python script can be created to generate the full JSON programmatically.

## Key Features

1. **Dual Data Sources**: Combines real-time Prometheus metrics with detailed Log Analytics logs
2. **Comprehensive Coverage**: Traffic, latency, security, performance, and health monitoring
3. **Actionable Insights**: Top URLs, slow requests, security threats, backend health
4. **Geographic Analysis**: Request distribution and latency by country
5. **Security Focus**: Detailed WAF analysis with blocked IPs and triggered rules
6. **Performance Metrics**: Latency percentiles, cache hit ratio, slow request tracking

## Notes

- Log Analytics panels require `logAnalyticsWorkspaceId` to be configured
- Prometheus panels work in all environments (direct Azure Monitor metrics)
- Time range variables (`$__timeFrom`, `$__timeTo`, `$__interval`) are Grafana built-ins
- All KQL queries are optimized for performance with time filters
