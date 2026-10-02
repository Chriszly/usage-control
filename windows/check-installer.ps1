# Checks the Windows installer on a Windows machine, as an administrator:
# installs it with the website on, updates it to a newer version without
# options and checks the options were kept, uninstalls it, then installs it
# with the defaults and checks it only serves the usage data.
#
#   pwsh windows/check-installer.ps1 -Msi usage-control-1.2.3-x64.msi -NewerMsi usage-control-1.2.4-x64.msi
param(
    [Parameter(Mandatory)] [string] $Msi,
    # The same installer with a higher version, to check an update.
    [Parameter(Mandatory)] [string] $NewerMsi
)

$ErrorActionPreference = 'Stop'
$Msi = (Resolve-Path $Msi).Path
$NewerMsi = (Resolve-Path $NewerMsi).Path

function Invoke-Installer([string] $Arguments) {
    $process = Start-Process msiexec.exe -ArgumentList "$Arguments /qn /l*v msiexec.log" -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        Get-Content msiexec.log -Tail 80
        throw "msiexec $Arguments failed with exit code $($process.ExitCode)"
    }
}

# Waits for the service to answer, as it takes a moment to start.
function Get-Answer([string] $Url) {
    foreach ($try in 1..30) {
        try { return Invoke-RestMethod $Url } catch { Start-Sleep -Seconds 1 }
    }
    Get-Service UsageControl | Format-List
    Get-EventLog -LogName Application -Source UsageControl -Newest 5 -ErrorAction SilentlyContinue | Format-List
    throw "The service did not answer $Url"
}

function Get-StatusCode([string] $Url) {
    (Invoke-WebRequest $Url -UseBasicParsing -SkipHttpErrorCheck).StatusCode
}

function Assert-FirewallPort([string] $Port) {
    $rule = Get-NetFirewallRule -DisplayName 'Usage Control'
    $opened = ($rule | Get-NetFirewallPortFilter).LocalPort
    if ($opened -ne $Port) { throw "The firewall rule opens port $opened, not $Port" }
    if ($rule.Profile -ne 'Private') { throw "The firewall rule applies to $($rule.Profile) networks, not only private ones" }
}

function Assert-Website([string] $Port) {
    $devices = Get-Answer "http://127.0.0.1:$Port/api/devices"
    $names = ($devices.devices | ForEach-Object { $_.name }) -join ', '
    if ($names -ne 'Runner, Pi') { throw "The website shows the devices '$names', not 'Runner, Pi'" }
    $status = Get-StatusCode "http://127.0.0.1:$Port/"
    if ($status -ne 200) { throw "The website answered $status" }
    if (-not (Test-Path "$env:ProgramData\Usage Control\usage-control.db")) { throw 'The history database was not created' }
    Assert-FirewallPort $Port
}

Write-Host 'Installing with the website on'
Invoke-Installer "/i `"$Msi`" PORT=8091 WEBSITE=1 DEVICE_NAME=Runner HUB_DEVICES=Pi=192.168.1.20:8080 RETENTION_DAYS=7"
$service = Get-Service UsageControl
if ($service.StartType -ne 'Automatic') { throw "The service starts $($service.StartType), not automatically" }
Assert-Website 8091

Write-Host 'Updating without options keeps them'
Invoke-Installer "/i `"$NewerMsi`""
Assert-Website 8091

Write-Host 'Uninstalling'
Invoke-Installer "/x `"$NewerMsi`""
if (Get-Service UsageControl -ErrorAction SilentlyContinue) { throw 'The service is still installed' }
if (Get-NetFirewallRule -DisplayName 'Usage Control' -ErrorAction SilentlyContinue) { throw 'The firewall rule is still there' }
if (Get-ItemProperty 'HKLM:\SOFTWARE\Usage Control' -Name PORT -ErrorAction SilentlyContinue) { throw 'The remembered options are still there' }

Write-Host 'Installing with the defaults serves only the usage data'
Invoke-Installer "/i `"$Msi`""
$metrics = Get-Answer 'http://127.0.0.1:8080/api/metrics'
$metrics | ConvertTo-Json -Depth 4
if ($metrics.disks.Count -lt 1) { throw 'The system disk is missing from the metrics' }
$status = Get-StatusCode 'http://127.0.0.1:8080/'
if ($status -ne 404) { throw "The website answered $status; without WEBSITE=1 it should be off" }
Assert-FirewallPort 8080
Invoke-Installer "/x `"$Msi`""

Write-Host 'The installer works'
