# Overview

PostgreSQL is used as the primary database for Clusters-Service and Maestro.

## Configuration

We expose the following Postgres resource settings in [configuration](../configuration.md)

| Top Level service | JSON Path in Configuration File |
|-------------------|---------------------------------|
| clusters-service  | `clustersService.postgres`      |
| maestro           | `maestro.postgres`              |

### Postgres settings

| Setting              | JSON Path                       | Description                                                       |
|----------------------|---------------------------------|-------------------------------------------------------------------|
| Deploy               | `.postgres.deploy`              | Whether to deploy Postgres (may be disabled in some environments) |
| Name                 | `.postgres.name`                | Name of the Azure Postgres resource                               |
| Private              | `.postgres.private`             | Whether to use a private endpoint for Postgres                    |
| Storage Size         | `.postgres.serverStorageSizeGB` | Size of the Postgres storage in GB                                |
| Server Version       | `.postgres.serverVersion`       | Version of the Postgres server to use                             |
| Minimum TLS Version  | `.postgres.minTLSVersion`       | Minimum TLS version for Postgres connections                      |
| Database Name        | `.postgres.databaseName`        | Name of the Postgres database to create on the server             |
| Zone Redundancy Mode | `.postgres.zoneRedundantMode`   | Zone redundancy mode for the Postgres server                      |

Refer to the [schema](../../config/config.schema.json) for more details on the configuration options

## Major version upgrade

[Refer to the azure docs for details](https://learn.microsoft.com/en-us/azure/postgresql/flexible-server/concepts-major-version-upgrade)

Azure Database for PostgreSQL Flexible Server officially treat major version upgrades as **offline operations** meaning this will incur downtime.

Overview:

* Set the application to maintenance mode
* Upgrade the Postgres server version
  * In ARO HCP this is done by updating the `serverVersion` in the configuration file and applying the configuration by running the pipeline
* Wait for the upgrade to complete
* Set the application back to normal mode
