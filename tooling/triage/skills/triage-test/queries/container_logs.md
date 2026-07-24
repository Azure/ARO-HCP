# Generic Container Logs

Whenever a bespoke table does not exist for the logs for a component, the raw logs can still be retrieved using the generic `containerLogs` table. Provide the namespace, pod name and container name to select specific logs, and write new queries that use `distinct` to find the possible values for these fields.