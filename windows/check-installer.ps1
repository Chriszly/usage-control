# Checks the Windows installer on a Windows machine, as an administrator:
# installs it with the website and the power and memory add-ons on, checks
# the tray icon pauses, resumes and stops the service, updates it to a newer
# version without options and checks the options and the add-ons were kept
# and the tray icon was closed for the update,
# uninstalls it, then installs it with the defaults and checks it only serves
# the usage data.
#
#   pwsh windows/check-installer.ps1 -Msi usage-control-1.2.3-x64.msi -NewerMsi usage-control-1.2.3a-x64.msi
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

# The tray icon's hidden window, which its menu commands are sent to.
Add-Type -Namespace Win32 -Name Tray -MemberDefinition @'
[DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern IntPtr FindWindow(string className, string windowName);
[DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr wParam, IntPtr lParam);
'@
$trayExe = "$env:ProgramFiles\Usage Control\usage-control-tray.exe"

function Wait-Until([scriptblock] $Condition, [string] $What) {
    foreach ($try in 1..30) {
        if (& $Condition) { return }
        Start-Sleep -Seconds 1
    }
    throw "Waited in vain until $What"
}

function Start-Tray {
    Start-Process $trayExe
    Wait-Until { [Win32.Tray]::FindWindow('SystrayClass', '') -ne [IntPtr]::Zero } 'the tray icon started'
}

# Clicks a menu entry of the tray icon. The entries are numbered in the order
# the tray program adds them: 7 is Pause, Resume or Start, 8 is Stop and exit.
function Invoke-TrayMenu([int] $Entry) {
    $window = [Win32.Tray]::FindWindow('SystrayClass', '')
    $null = [Win32.Tray]::PostMessage($window, 0x0111, [IntPtr]$Entry, [IntPtr]::Zero)
}

function Assert-Tray {
    if (-not (Test-Path $trayExe)) { throw 'The tray program is missing' }
    $run = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name 'Usage Control').'Usage Control'
    if ($run -ne "`"$trayExe`"") { throw "The tray icon starts at login as '$run', not '`"$trayExe`"'" }
    $rights = (sc.exe sdshow UsageControl) -join ''
    if ($rights -notlike '*(A;;CCLCSWRPWPLOCRRC;;;IU)*') { throw "Logged-in users may not start and stop the service: $rights" }

    Start-Tray
    # The tray checks the service every 5 seconds; each wait gives it time to
    # notice the change before the next click.
    Invoke-TrayMenu 7
    Wait-Until { (Get-Service UsageControl).Status -eq 'Stopped' } 'Pause stopped the service'
    Start-Sleep -Seconds 6
    Invoke-TrayMenu 7
    Wait-Until { (Get-Service UsageControl).Status -eq 'Running' } 'Resume started the service'
    Start-Sleep -Seconds 6
    Invoke-TrayMenu 8
    Wait-Until { (Get-Service UsageControl).Status -eq 'Stopped' -and -not (Get-Process usage-control-tray -ErrorAction SilentlyContinue) } 'Stop and exit stopped the service and closed the icon'
    Start-Service UsageControl
}

function Assert-PowerAddOn {
    $addOn = Get-Service UsageControlPower -ErrorAction SilentlyContinue
    if (-not $addOn) { throw 'The power add-on is not installed' }
    Wait-Until { (Get-Service UsageControlPower).Status -eq 'Running' } 'the power add-on runs'
    Wait-Until { Test-Path "$env:ProgramData\Usage Control\addons\power.json" } 'the power add-on wrote its report'
}

# The memory add-on reads the Memory performance counters as LocalService;
# its report holds the committed memory once it could read them.
function Assert-MemoryAddOn {
    $addOn = Get-Service UsageControlMemory -ErrorAction SilentlyContinue
    if (-not $addOn) { throw 'The memory add-on is not installed' }
    Wait-Until { (Get-Service UsageControlMemory).Status -eq 'Running' } 'the memory add-on runs'
    $report = "$env:ProgramData\Usage Control\addons\memory.json"
    Wait-Until {
        try {
            $items = (Get-Content $report -Raw -ErrorAction Stop | ConvertFrom-Json).extras.items
            @($items | Where-Object { $_.id -eq 'committed' -and $_.value -gt 0 }).Count -eq 1
        } catch { $false }
    } 'the memory add-on wrote the committed memory'
}

Write-Host 'Installing with the website and the power and memory add-ons on'
Invoke-Installer "/i `"$Msi`" PORT=8091 WEBSITE=1 DEVICE_NAME=Runner HUB_DEVICES=Pi=192.168.1.20:9393 RETENTION_DAYS=7 POWER=1 MEMORY=1"
$service = Get-Service UsageControl
if ($service.StartType -ne 'Automatic') { throw "The service starts $($service.StartType), not automatically" }
Assert-Website 8091
Assert-PowerAddOn
Assert-MemoryAddOn

Write-Host 'The tray icon pauses, resumes and stops the service'
Assert-Tray
Assert-Website 8091

Write-Host 'Updating without options keeps them and closes the tray icon'
Start-Tray
Invoke-Installer "/i `"$NewerMsi`""
if (Get-Process usage-control-tray -ErrorAction SilentlyContinue) { throw 'The update left the old tray icon running' }
$installed = @(Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*' |
    Where-Object { $_.PSObject.Properties['DisplayName'] -and $_.DisplayName -eq 'Usage Control' })
if ($installed.Count -ne 1) { throw "The update left $($installed.Count) installs of Usage Control, not 1" }
Assert-Website 8091
Assert-PowerAddOn
Assert-MemoryAddOn

Write-Host 'Uninstalling'
Invoke-Installer "/x `"$NewerMsi`""
if (Get-Service UsageControl -ErrorAction SilentlyContinue) { throw 'The service is still installed' }
if (Get-Service UsageControlPower -ErrorAction SilentlyContinue) { throw 'The power add-on is still installed' }
if (Get-Service UsageControlMemory -ErrorAction SilentlyContinue) { throw 'The memory add-on is still installed' }
if (Get-NetFirewallRule -DisplayName 'Usage Control' -ErrorAction SilentlyContinue) { throw 'The firewall rule is still there' }
if (Get-ItemProperty 'HKLM:\SOFTWARE\Usage Control' -Name PORT -ErrorAction SilentlyContinue) { throw 'The remembered options are still there' }
if (Test-Path $trayExe) { throw 'The tray program is still there' }
if (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name 'Usage Control' -ErrorAction SilentlyContinue) { throw 'The tray icon still starts at login' }

Write-Host 'Installing with the defaults serves only the usage data'
Invoke-Installer "/i `"$Msi`""
$metrics = Get-Answer 'http://127.0.0.1:9393/api/metrics'
$metrics | ConvertTo-Json -Depth 4
if ($metrics.disks.Count -lt 1) { throw 'The system disk is missing from the metrics' }
$status = Get-StatusCode 'http://127.0.0.1:9393/'
if ($status -ne 404) { throw "The website answered $status; without WEBSITE=1 it should be off" }
Assert-FirewallPort 9393
if (Get-Service UsageControlPower -ErrorAction SilentlyContinue) { throw 'The power add-on was installed without POWER=1' }
if (Get-Service UsageControlMemory -ErrorAction SilentlyContinue) { throw 'The memory add-on was installed without MEMORY=1' }
Invoke-Installer "/x `"$Msi`""

Write-Host 'The installer works'
