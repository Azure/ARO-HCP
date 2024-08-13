# The overall API design for the HCP RP

The HCP API design follows the design of ARO Classic API.
This can be consulted at [Azure/azure-rest-api-specs](https://github.com/Azure/azure-rest-api-specs/tree/main/specification/redhatopenshift)


## Subscription lifecycle

The subscription lifecycle is the same as the ARO Classic API, which means the server
needs to implement the following operation.

It is not exposed in the API specification, but it is needed to be implemented.

```
PUT /subscriptions/{subscriptionId}

{
	"subscription": {
		"state": "Registered|Unregistered|Warned|Suspended|Deleted"
		"properties": {
			"tenantId": string,
			"accountOwner": {
				"email": string
			},
			"registeredFeatures": [
				"name": string,
				"state": string
			]
		}
	}
}
```

This operation is used to register the subscription in the HCP RP and is exposed in ARM manifest.


## Preflight checks

The preflight checks are used to run some validations before creating the HCP cluster.
These are not exposed in the swagger API specification, but need to be implemented.

More information can be found at [armwiki/preflight](https://armwiki.azurewebsites.net/fundamentals/control_plane_kpis/preflight.html)

```
POST /subscriptions/{subscriptionId}/resourcegroups/{resourceGroupName}/providers/{resourceProviderNamespace}/deployments/{deploymentName}/preflight?api-version={api-version}
```

How this is implemented in ARO classic can be found at [ARO-RP/frontend/preflightvalidation](https://github.com/Azure/ARO-RP/blob/a37c544461ca45b612c477235658e7a2973dca80/pkg/frontend/openshiftcluster_preflightvalidation.go).