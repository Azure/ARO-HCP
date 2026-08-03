using System.ComponentModel.DataAnnotations;

namespace GenevaHealthFunctionSample.Configuration;

public sealed class GenevaOptions
{
    [Required]
    public string Environment { get; init; } = "INT";

    [Required]
    public string KeyVaultUrl { get; init; } = string.Empty;

    [Required]
    public string KeyVaultCertificateName { get; init; } = string.Empty;

    public string? KeyVaultCertificateVersion { get; init; }

    [Required]
    public string MonitoringAccountName { get; init; } = string.Empty;

    [Required]
    public string ResourceTypeName { get; init; } = "RoleInstance";

    [Required]
    public string DefaultApp { get; init; } = "GenevaHealthFunction";

    [Required]
    public string DefaultRegion { get; init; } = "West US";

    [Required]
    public string DefaultDatacenter { get; init; } = "CO2";

    [Required]
    public string DefaultRoleInstance { get; init; } = "CO2BFK083";

    [Required]
    public string DefaultWatchdogName { get; init; } = "FunctionWatchdog";

    [Range(1, 10)]
    public int RetryCount { get; init; } = 3;

    [Range(1, 30)]
    public int RetrySleepIntervalSeconds { get; init; } = 1;

    [Range(1, 1000)]
    public int BatchSize { get; init; } = 20;

    [Range(1, 500)]
    public int MaxConcurrentConnections { get; init; } = 20;

    [Range(1, 24)]
    public int ConnectionCacheHours { get; init; } = 6;

    [Range(0, 4)]
    public int DefaultSeverity { get; init; } = 2;

    [Range(30, 86400)]
    public int DefaultReportExpirationSeconds { get; init; } = 120;
}
