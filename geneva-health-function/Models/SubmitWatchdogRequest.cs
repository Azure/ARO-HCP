namespace GenevaHealthFunctionSample.Models;

public sealed class SubmitWatchdogRequest
{
    public string? Status { get; init; }

    public string? Message { get; init; }

    public string? WatchdogName { get; init; }

    public string? ResourceTypeName { get; init; }

    public Dictionary<string, string>? ResourceDimensions { get; init; }

    public string? App { get; init; }

    public string? Region { get; init; }

    public string? Datacenter { get; init; }

    public string? RoleInstance { get; init; }
}
