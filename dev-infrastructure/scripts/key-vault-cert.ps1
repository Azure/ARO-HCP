param (
    [string]$IssuerName,

    [string]$VaultName,

    [string]$CertName,

    [string]$SubjectName,

    [string]$DnsNames,

    [int]$ValidityInMonths = 12,

    [int]$RenewAtPercentageLifetime = 24,

    [string]$SecretContentType = 'application/x-pkcs12',

    [bool]$Force
)

try
{
    Write-Output "`nUTC is: $(Get-Date)"

    $updateCertificate = $Force

    $DNSNamesArray = $DnsNames -split '_'

    Write-Output $DNSNamesArray

    $c = Get-AzContext -ErrorAction stop
    if ($c)
    {
        Write-Output "`nContext is: "
        $c | Select-Object Account, Subscription, Tenant, Environment | Format-List | Out-String

        $DNSNamesArray = $DnsNames -split '_'

        Write-Output $DNSNamesArray

        # RFC 5280 requires that the common name be <= 64 characters
        if ($SubjectName -match '^CN=(.+)$') {
            $cn = $matches[1]
            if ($cn.Length -gt 64) {
                throw "CN length violates RFC 5280, it must be less than or equal to 64 characters."
            }
        }

        $PolicyParams = @{
            RenewAtPercentageLifetime = $RenewAtPercentageLifetime
            SecretContentType         = $SecretContentType
            ValidityInMonths          = $ValidityInMonths
            IssuerName                = $IssuerName
            SubjectName               = $SubjectName
            DnsNames                  = $DNSNamesArray
            KeyUsage                  = @('DigitalSignature', 'KeyEncipherment')
        }

        $Cert = Get-AzKeyVaultCertificate -VaultName $VaultName -Name $CertName
        If ($Cert)
        {
            $ExistingPolicy = $Cert | Get-AzKeyVaultCertificatePolicy | Where-Object SubjectName -EQ $SubjectName

            # Check if policy parameters have changed
            if ($ExistingPolicy)
            {
                Write-Warning -Message "Policy exists      [$($ExistingPolicy.SubjectName)]"

                $policyChanged = (
                    $ExistingPolicy.RenewAtPercentageLifetime -ne $RenewAtPercentageLifetime -or
                    $ExistingPolicy.SecretContentType -ne $SecretContentType -or
                    $ExistingPolicy.ValidityInMonths -ne $ValidityInMonths -or
                    $ExistingPolicy.IssuerName -ne $IssuerName -or
                    (Compare-Object -ReferenceObject $ExistingPolicy.DnsNames -DifferenceObject $DNSNamesArray)
                )

                if ($policyChanged)
                {
                    Write-Warning -Message "Policy parameters changed, certificate will be updated"
                    $updateCertificate = $true
                }
            }
        }

        $Policy = New-AzKeyVaultCertificatePolicy @PolicyParams

        if ($Cert -and (-not $updateCertificate))
        {
            Write-Warning -Message "Certificate exists [$($Cert.Name)]"
        }
        else
        {
            Write-Warning -Message "Creating Certificate [$CertName]"
            $Result = Add-AzKeyVaultCertificate -VaultName $VaultName -Name $CertName -CertificatePolicy $Policy
            $Result.StatusDetails
            while ($New.Enabled -ne $true)
            {
                $New = Get-AzKeyVaultCertificate -VaultName $VaultName -Name $CertName
                Start-Sleep -Seconds 30
            }
        }

        $out = $cert ?? $new

        $DeploymentScriptOutputs = @{}
        $DeploymentScriptOutputs['KeyVaultCertId'] = $out.Id
        $DeploymentScriptOutputs['Thumbprint'] = $out.Thumbprint
        $thumbHex = $out.Certificate.Thumbprint -replace '[:\s]', ''
        $thumbBytes = for ($i = 0; $i -lt $thumbHex.Length; $i += 2) {
            [Convert]::ToByte($thumbHex.Substring($i, 2), 16)
        }
        $DeploymentScriptOutputs['KeyIdentifier'] = [Convert]::ToBase64String($thumbBytes)
        $DeploymentScriptOutputs['PublicKey'] = [System.Convert]::ToBase64String($out.Certificate.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Cert))
        $DeploymentScriptOutputs['NotBefore'] = (Get-Date $out.Certificate.NotBefore.ToUniversalTime() -Format "yyyy-MM-ddTHH:mm:ssZ")
        $DeploymentScriptOutputs['NotAfter'] = (Get-Date $out.Certificate.NotAfter.ToUniversalTime()  -Format "yyyy-MM-ddTHH:mm:ssZ")

        if ($IssuerName -eq 'Self')
        {
            $base64Cert = [System.Convert]::ToBase64String($out.Certificate.Export('Cert'))
            $pemCert = "-----BEGIN CERTIFICATE-----`n$base64Cert`n-----END CERTIFICATE-----"
            $DeploymentScriptOutputs['CACert'] = $pemCert
        }

    }
    else
    {
        throw 'Cannot get a context'
    }
}
catch
{
    Write-Warning $_
    Write-Warning $_.exception
}
