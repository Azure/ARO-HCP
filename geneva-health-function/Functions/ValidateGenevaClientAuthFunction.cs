// TODO: re-enable once Microsoft.Cloud.HealthService.Client NuGet package is accessible
/*
using System.Net;
using GenevaHealthFunctionSample.Configuration;
using GenevaHealthFunctionSample.Services;
using Microsoft.Azure.Functions.Worker;
using Microsoft.Azure.Functions.Worker.Http;
using Microsoft.Cloud.HealthService.Client;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;

namespace GenevaHealthFunctionSample.Functions;

public sealed class ValidateGenevaClientAuthFunction
{
    private readonly GenevaHealthClientFactory _factory;
    private readonly GenevaOptions _options;
    private readonly ILogger<ValidateGenevaClientAuthFunction> _logger;

    public ValidateGenevaClientAuthFunction(
        GenevaHealthClientFactory factory,
        IOptions<GenevaOptions> options,
        ILogger<ValidateGenevaClientAuthFunction> logger)
    {
        _factory = factory;
        _options = options.Value;
        _logger = logger;
    }

    [Function("ValidateGenevaClientAuth")]
    public async Task<HttpResponseData> Run(
        [HttpTrigger(AuthorizationLevel.Function, "get", "post")] HttpRequestData req,
        CancellationToken cancellationToken)
    {
        var resourceId = new ResourceIdentifier(
            _options.MonitoringAccountName,
            _options.ResourceTypeName,
            new Dictionary<string, object>
            {
                ["App"] = _options.DefaultApp,
                ["Region"] = _options.DefaultRegion,
                ["Datacenter"] = _options.DefaultDatacenter,
                ["RoleInstance"] = _options.DefaultRoleInstance
            });

        try
        {
            var client = _factory.Create();
            var health = await client.GetHealthData(resourceId);

            var response = req.CreateResponse(HttpStatusCode.OK);
            await response.WriteAsJsonAsync(new
            {
                worked = true,
                message = "Geneva SDK client worked with auth success.",
                account = _options.MonitoringAccountName,
                environment = _options.Environment,
                resourceType = _options.ResourceTypeName,
                app = _options.DefaultApp,
                region = _options.DefaultRegion,
                datacenter = _options.DefaultDatacenter,
                roleInstance = _options.DefaultRoleInstance,
                currentHealthStatus = health?.HealthStatus.ToString()
            }, cancellationToken);

            _logger.LogInformation(
                "Geneva SDK auth validated for account {Account} and app {App}",
                _options.MonitoringAccountName,
                _options.DefaultApp);

            return response;
        }
        catch (Exception ex)
        {
            _logger.LogError(ex, "Failed to validate Geneva SDK auth.");
            var error = req.CreateResponse(HttpStatusCode.InternalServerError);
            await error.WriteAsJsonAsync(new
            {
                worked = false,
                error = ex.Message
            }, cancellationToken);
            return error;
        }
    }
}
*/
