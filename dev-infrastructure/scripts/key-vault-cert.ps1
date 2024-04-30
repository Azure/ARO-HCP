param (
    [string]$IssuerName,

    [string]$VaultName,
    
    [string]$CertName,

    [string]$SubjectName,

    [string]$DnsNames,

    [int]$ValidityInMonths = 12,

    [int]$RenewAtPercentageLifetime = 24,

    [string]$SecretContentType = 'application/x-pkcs12',

    [switch]$Disabled,

    [bool]$Force
)

try
{
    Write-Output "`nUTC is: $(Get-Date)"

    $DNSNamesArray = $DnsNames -split '_'

    Write-Output $DNSNamesArray

    $c = Get-AzContext -ErrorAction stop
    if ($c)
    {
        Write-Output "`nContext is: "
        $c | Select-Object Account, Subscription, Tenant, Environment | Format-List | Out-String

        $DNSNamesArray = $DnsNames -split '_'

        Write-Output $DNSNamesArray

        $PolicyParams = @{
            RenewAtPercentageLifetime = $RenewAtPercentageLifetime
            SecretContentType         = $SecretContentType
            ValidityInMonths          = $ValidityInMonths
            IssuerName                = $IssuerName
            Disabled                  = $Disabled
            SubjectName               = $SubjectName
            DnsNames                  = $DNSNamesArray
            KeyUsage                  = @('DigitalSignature', 'KeyEncipherment')
        }

        $Cert = Get-AzKeyVaultCertificate -VaultName $VaultName -Name $CertName
        If ($Cert)
        {
            $Policy = $Cert | Get-AzKeyVaultCertificatePolicy | Where-Object SubjectName -EQ $SubjectName
        }

        if ($Policy)
        {
            Write-Warning -Message "Policy exists      [$($policy.SubjectName)]"
            if ($Force)
            {
                Write-Warning -Message "Force Policy [$($policy.SubjectName)] settings"
                $Policy = New-AzKeyVaultCertificatePolicy @PolicyParams
            }
        }
        else
        {
            Write-Warning -Message "Creating Policy [$SubjectName]"
            $Policy = New-AzKeyVaultCertificatePolicy @PolicyParams
        }

        if ($Cert -and (-not $Force))
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
