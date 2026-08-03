# Geneva Health SDK Azure Function (INT)

This sample is a basic Azure Function (HTTP trigger) that validates Geneva SDK client/auth connectivity against a Geneva Health monitoring account in INT.

## Prerequisites

- .NET 8 SDK
- Azure Functions Core Tools v4
- Access to internal Geneva package feeds in `nuget.config`
- A client certificate authorized in your monitoring account

## Configure

1. Update `local.settings.json` under `Geneva`:
   - `Environment` should be `INT`
   - `ClientCertificateThumbprint`
   - `MonitoringAccountName`
   - Any default dimensions and watchdog metadata
2. Ensure your certificate is installed in `LocalMachine\\My` if `UseCurrentUserStore=false`; otherwise in `CurrentUser\\My`.

## Deploy and validate in INT

This validation is expected to run from a Function App hosted in your INT-connected environment, not from localhost.

1. Deploy this project to your Azure Function App.
2. In the Function App configuration, set the same `Geneva:*` settings from `local.settings.json`:
   - `Geneva:Environment=INT`
   - `Geneva:ClientCertificateThumbprint`
   - `Geneva:MonitoringAccountName`
   - default resource dimensions (`App`, `Region`, `Datacenter`, `RoleInstance`)
3. Ensure the certificate for `Geneva:ClientCertificateThumbprint` is available to the app runtime.

## Call the deployed function

```bash
curl "https://<your-function-app>.azurewebsites.net/api/ValidateGenevaClientAuth?code=<function-key>"
```

Expected success response includes `worked: true` and `Geneva SDK client worked with auth success.`.
