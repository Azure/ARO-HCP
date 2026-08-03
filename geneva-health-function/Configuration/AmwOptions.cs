using System.ComponentModel.DataAnnotations;

namespace GenevaHealthFunctionSample.Configuration;

public sealed class AmwOptions
{
    [Required]
    public string PrometheusQueryEndpoint { get; init; } = string.Empty;

    [Range(5, 120)]
    public int QueryTimeoutSeconds { get; init; } = 30;
}
