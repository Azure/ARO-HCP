// TODO: re-enable once Microsoft.Cloud.HealthService.Client NuGet package is accessible
/*
using GenevaHealthFunctionSample.Configuration;
using System.Security.Cryptography.X509Certificates;
using Azure.Identity;
using Azure.Security.KeyVault.Certificates;
using Azure.Security.KeyVault.Secrets;
using Microsoft.Cloud.HealthService.Client;
using Microsoft.Cloud.HealthService.Client.Configuration;
using Microsoft.Extensions.Options;

namespace GenevaHealthFunctionSample.Services;

public sealed class GenevaHealthClientFactory
{
    private readonly GenevaOptions _options;
    private readonly DefaultAzureCredential _credential;

    public GenevaHealthClientFactory(IOptions<GenevaOptions> options)
    {
        _options = options.Value;
        _credential = new DefaultAzureCredential();
    }

    public HealthServiceClient Create()
    {
        var env = ParseEnvironment(_options.Environment);
        var certificate = GetCertificateFromKeyVault();

        return new HealthServiceClient(
            env,
            certificate,
            (uint)_options.RetryCount,
            (uint)_options.RetrySleepIntervalSeconds,
            _options.BatchSize,
            _options.MaxConcurrentConnections,
            TimeSpan.FromHours(_options.ConnectionCacheHours));
    }

    private X509Certificate2 GetCertificateFromKeyVault()
    {
        var vaultUri = new Uri(_options.KeyVaultUrl);
        var certificateClient = new CertificateClient(vaultUri, _credential);

        Uri? secretId;
        if (string.IsNullOrWhiteSpace(_options.KeyVaultCertificateVersion))
        {
            KeyVaultCertificateWithPolicy latestCertificate = certificateClient.GetCertificate(_options.KeyVaultCertificateName).Value;
            secretId = latestCertificate.SecretId;
        }
        else
        {
            KeyVaultCertificate certificateVersion = certificateClient.GetCertificateVersion(
                _options.KeyVaultCertificateName,
                _options.KeyVaultCertificateVersion).Value;
            secretId = certificateVersion.SecretId;
        }

        var secretClient = new SecretClient(vaultUri, _credential);
        if (secretId is null)
        {
            throw new InvalidOperationException("The Key Vault certificate does not expose a secret identifier.");
        }

        var secretVersion = secretId.Segments[^1].Trim('/');
        KeyVaultSecret secret = secretClient.GetSecret(_options.KeyVaultCertificateName, secretVersion);
        var certBytes = Convert.FromBase64String(secret.Value);

        return new X509Certificate2(
            certBytes,
            (string?)null,
            X509KeyStorageFlags.MachineKeySet | X509KeyStorageFlags.Exportable | X509KeyStorageFlags.EphemeralKeySet);
    }

    private static ExecutionEnvironment ParseEnvironment(string raw)
    {
        if (Enum.TryParse<ExecutionEnvironment>(raw, ignoreCase: true, out var parsed))
        {
            return parsed;
        }

        return ExecutionEnvironment.Int;
    }
}
*/
