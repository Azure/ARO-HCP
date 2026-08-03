// TODO: re-enable once Microsoft.Cloud.HealthService.Client NuGet package is accessible
/*
using System.Net;
using System.Text.Json;
using GenevaHealthFunctionSample.Configuration;
using GenevaHealthFunctionSample.Models;
using GenevaHealthFunctionSample.Services;
using Microsoft.Azure.Functions.Worker;
using Microsoft.Azure.Functions.Worker.Http;
using Microsoft.Cloud.HealthService.Client;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Microsoft.Online.RecoveryService.Contract.Models;

namespace GenevaHealthFunctionSample.Functions;

public sealed class SubmitGenevaWatchdogFunction
{
    private readonly GenevaHealthClientFactory _factory;
    private readonly GenevaOptions _options;
    private readonly ILogger<SubmitGenevaWatchdogFunction> _logger;

    public SubmitGenevaWatchdogFunction(
        GenevaHealthClientFactory factory,
        IOptions<GenevaOptions> options,
        ILogger<SubmitGenevaWatchdogFunction> logger)
    {
        _factory = factory;
        _options = options.Value;
        _logger = logger;
    }

    [Function("SubmitGenevaWatchdog")]
    public async Task<HttpResponseData> Run(
        [HttpTrigger(AuthorizationLevel.Function, "post")] HttpRequestData req,
        CancellationToken cancellationToken)
    {
        SubmitWatchdogRequest? payload;
        try
        {
            payload = await JsonSerializer.DeserializeAsync<SubmitWatchdogRequest>(
                req.Body,
                cancellationToken: cancellationToken);
        }
        catch (JsonException)
        {
            var badJsonResponse = req.CreateResponse(HttpStatusCode.BadRequest);
            await badJsonResponse.WriteAsJsonAsync(new { error = "Invalid JSON body." }, cancellationToken);
            return badJsonResponse;
        }

        var status = ParseStatus(payload?.Status);
        var message = string.IsNullOrWhiteSpace(payload?.Message)
            ? "Watchdog report from Azure Function"
            : payload!.Message!;

        var app = string.IsNullOrWhiteSpace(payload?.App) ? _options.DefaultApp : payload!.App!;
        var region = string.IsNullOrWhiteSpace(payload?.Region) ? _options.DefaultRegion : payload!.Region!;
        var datacenter = string.IsNullOrWhiteSpace(payload?.Datacenter) ? _options.DefaultDatacenter : payload!.Datacenter!;
        var roleInstance = string.IsNullOrWhiteSpace(payload?.RoleInstance) ? _options.DefaultRoleInstance : payload!.RoleInstance!;
        var watchdogName = string.IsNullOrWhiteSpace(payload?.WatchdogName) ? _options.DefaultWatchdogName : payload!.WatchdogName!;

        var resourceId = new ResourceIdentifier(
            _options.MonitoringAccountName,
            _options.ResourceTypeName,
            new Dictionary<string, object>
            {
                ["App"] = app,
                ["Region"] = region,
                ["Datacenter"] = datacenter,
                ["RoleInstance"] = roleInstance
            });

        var report = new WatchdogReport
        {
            WatchdogName = watchdogName,
            WatchdogType = WatchdogType.Periodic,
            ResourceId = resourceId,
            Status = status,
            Message = message,
            WatchdogMetadataCollection = new MetadataCollection(new Dictionary<string, object>
            {
                ["Severity"] = _options.DefaultSeverity,
                ["ReportExpirationTime"] = _options.DefaultReportExpirationSeconds,
                ["Title"] = "Submitted from Azure Function"
            })
        };

        try
        {
            var client = _factory.Create();
            var failed = await client.SubmitBatchWatchdogHealthReports(new List<WatchdogReport> { report });

            var response = req.CreateResponse(HttpStatusCode.OK);
            await response.WriteAsJsonAsync(new
            {
                submitted = true,
                failures = failed.Count,
                account = _options.MonitoringAccountName,
                resourceType = _options.ResourceTypeName,
                app,
                region,
                datacenter,
                roleInstance,
                watchdogName,
                status = status.ToString()
            }, cancellationToken);

            _logger.LogInformation(
                "Geneva watchdog submitted. Failures: {FailureCount}, Account: {Account}, App: {App}",
                failed.Count,
                _options.MonitoringAccountName,
                app);

            return response;
        }
        catch (Exception ex)
        {
            _logger.LogError(ex, "Failed to submit Geneva watchdog report.");
            var error = req.CreateResponse(HttpStatusCode.InternalServerError);
            await error.WriteAsJsonAsync(new
            {
                submitted = false,
                error = ex.Message
            }, cancellationToken);
            return error;
        }
    }

    private static ResourceHealthStatus ParseStatus(string? raw)
    {
        if (string.IsNullOrWhiteSpace(raw))
        {
            return ResourceHealthStatus.Healthy;
        }

        return Enum.TryParse<ResourceHealthStatus>(raw, ignoreCase: true, out var parsed)
            ? parsed
            : ResourceHealthStatus.Healthy;
    }
}
*/
