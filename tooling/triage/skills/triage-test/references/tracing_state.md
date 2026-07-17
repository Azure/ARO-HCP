# Tracing State Through The System

When investigating a failed interaction with the ARO HCP RP, identify the noun and verb involved - that is, what is the ARM provider type in question and are we issuing a request against that type, or against a long-running operation?

As the resource provider turns customer intent into realized outputs, the customer's specified intent and the real status of the system will be encoded by each server separately. Most of our systems periodically dump their state into logs, so it's possible to view the evolution of an internal representation over time. Debugging a flow requires that we review the internal state as it evolves and identify the actors (controllers) inside each server that emit useful logs while processing the state.

In general, review controller status as a first approach - top-level issues should be divulged there. Only if those don't clearly identify the issue do we need to look at controller logs. If conditions and logs don't help, dig into the state representation and see how it evolves over time.

## Normal Tracing Methodology

### Client to RP Frontend

Connect a specific client request to the RP Frontend logs pertaining to it with the correlation ID, provided by the server to the client in the `X-Ms-Correlation-Request-Id` header. Query the frontend for these logs with:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('frontendLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where resource_group == '{{ .ResourceGroup }}'
| where correlation_request_id == '{{ .Extra.CorrelationID }}'
| project timestamp, msg, log.error, request_method, request_path, request_query, response_status_code
```

If no explicit correlation ID is visible from the client side, remember that tests run in independent resource groups, so filtering by resource group is sufficient:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('frontendLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where resource_group == '{{ .ResourceGroup }}'
| project timestamp, msg, log.error, request_method, request_path, request_query, response_status_code
```

The frontend stores no state.

### RP Frontend To RP Backend

Determine the resource type associated with the frontend call - this should be extracted from the URI after the Microsoft.RedHatOpenShift segment. If unclear, further research can be done with the frontend route configuration at `frontend/pkg/frontend/routes.go`.

Use the following query to view the associated controller statuses for the resource:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('backendLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'datadump'
| where log.controllerKey.resourceGroupName == '{{ .ResourceGroup }}'
| where log.content.resourceType =~ '{{ .Extra.ResourceType }}/hcpopenshiftcontrollers'
| distinct tostring(log.content)
| extend content = parse_json(log_content)
| extend controller_name = extract("/hcpOpenShiftControllers/([^\\/]+)", 1, tostring(content.resourceID))
| mv-expand condition = content.properties.status.conditions
| project todatetime(condition.lastTransitionTime), controller_name, condition.type, condition.status, condition.reason, condition.message
| distinct todatetime(condition_lastTransitionTime), controller_name, tostring(condition_type), tostring(condition_status), tostring(condition_reason), tostring(condition_message)
| order by condition_lastTransitionTime asc
```

If the controller statuses do not help understand the issue, review the controllers in `backend/pkg/app/backend.go` to determine which controllers pertain to the issue at hand and query their output specifically:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('backendLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where container_name == 'aro-hcp-backend'
| where log.controller_name == '{{ .Extra.ControllerName }}'
| where log.controllerKey.resourceGroupName == '{{ .ResourceGroup }}'
| project log
```

Use the following query to view the state of the customer-facing resource over time:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('backendLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'datadump'
| where log.controllerKey.resourceGroupName == '{{ .ResourceGroup }}'
| where log.content.resourceType =~ '{{ .Extra.ResourceType }}'
| distinct tostring(log.content)
```

If the customer-facing object does not contain sufficient information, review the internal representation. These are stored under a separate resource type. Query for all available resource types like:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('backendLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'datadump'
| where log.controllerKey.resourceGroupName == '{{ .ResourceGroup }}'
| distinct tolower(tostring(log.content.resourceType))
```

Internal representation are sub-resources with `serviceprovider` prefixes on their last segment. Identify the internal resource type and query to view the state over time:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('backendLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'datadump'
| where log.controllerKey.resourceGroupName == '{{ .ResourceGroup }}'
| where log.content.resourceType =~ '{{ .Extra.InternalResourceType }}'
| distinct tostring(log.content)
```

## RP Backend to Clusters-Service

While the RP frontend and backend use the Azure Resource ID, Clusters-Service has its own identifier, the cluster id (cid). Clusters-service associates all logs for all resource types with the top-level cluster ID, so use the following query to connect an ARO HCP cluster to the clusters-service cluster ID:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('backendLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'datadump'
| where log.controllerKey.resourceGroupName == '{{ .ResourceGroup }}'
| where log.content.resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters'
| distinct tostring(log.content)
| extend content = parse_json(log_content)
| distinct trim_start(@"^/api/aro_hcp/v1alpha1/clusters/", tostring(content.properties.internalId))
```

Then, query the clusters-service logs using this discriminant:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('clustersServiceLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where cid == '{{ .Extra.ClustersServiceId }}'
| project timestamp, log.msg
```

Use the following query to review the clusters-service internal state representation over time:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('backendLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'csstatedump'
| where log.controllerKey.resourceGroupName == '{{ .ResourceGroup }}'
| distinct tostring(log.csCluster)
```

## Clusters-Service to Maestro

Clusters-Service encodes the customer's intent into a HyperShift `HostedCluster` document and transports it to the management cluster via Maestro. It's not yet clear how to determine the document ID in Maestro that is used, to determine if Maestro correctly ran the transport.

## Maestro to HyperShift

HyperShift does the work to run the ARO HCP cluster and posts status to the `HostedCluster` object. You may review the `HostedCluster` conditions with:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('backendLogs')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where container_name == 'aro-hcp-backend'
| where log.controller_name == 'datadump'
| where log.controllerKey.resourceGroupName == '{{ .ResourceGroup }}'
| where log.content.resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters/managementclustercontents'
| distinct tostring(log.content)
| extend content = parse_json(log_content)
| mv-expand manifest = content.properties.status.kubeContent.items
| where manifest.kind == 'HostedCluster'
| mv-expand condition = manifest.status.conditions
| project condition.type, condition.status, condition.reason, condition.message, todatetime(condition.lastTransitionTime)
| distinct tostring(condition_type), tostring(condition_status), tostring(condition_reason), tostring(condition_message), todatetime(condition_lastTransitionTime)
| order by condition_lastTransitionTime asc
```

HyperShift controller logs are rarely useful, but the status on the `HostedCluster` object always is.

### Special Note on Admin Credentials

Admin credentials are minted by the PKI Operator, which emits useful events in a namespace associated with the clusters-service cluster ID that describe what it's doing:

```kql
cluster('{{ .ClusterURI }}').database('{{ .ServiceDatabase }}').table('kubernetesEvents')
| where timestamp between({{ kqlDatetime .StartTime }} .. {{ kqlDatetime .EndTime }})
| where eventNamespace has '{{ .Extra.ClustersServiceId }}'
| where objectName == 'control-plane-pki-operator'
| project timestamp, objectKind, objectName, reason, message, firstSeen, lastSeen
| distinct timestamp, objectKind, objectName, reason, message, firstSeen, lastSeen
```