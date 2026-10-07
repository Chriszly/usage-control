# Checks the Windows installer on a Windows machine, as an administrator:
# installs it with the website and the power, gpu, kernel, pressure, Wi-Fi,
# memory, ports, smart and processes add-ons on, checks the tray icon
# pauses, resumes and stops the service, updates it to a newer version
# without options and checks the options and the add-ons were kept, except
# RESET_PASSWORD, and the tray icon was closed for the update, repairs it
# with RESET_PASSWORD=true and again without, as the docs say to,
# uninstalls it, then installs it with the defaults and checks it only serves
# the usage data, and last checks that an update with WEBSITE=0 turns the
# website off.
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

# Checks a setting the installer gave the service, such as DATA_ONLY=true.
function Assert-ServiceSetting([string] $Name, [string] $Value) {
    $environment = (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Services\UsageControl').Environment
    $lines = @($environment | Where-Object { $_.StartsWith("$Name=") })
    if ($lines.Count -ne 1) { throw "The service has $($lines.Count) settings $Name, not 1: $($environment -join '; ')" }
    $got = $lines[0].Substring($Name.Length + 1)
    if ($got -ne $Value) { throw "The service's $Name is '$got', not '$Value'" }
}

function Get-ServiceProcessId {
    (Get-CimInstance Win32_Service -Filter "Name = 'UsageControl'").ProcessId
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

function Assert-GpuAddOn {
    $addOn = Get-Service UsageControlGPU -ErrorAction SilentlyContinue
    if (-not $addOn) { throw 'The gpu add-on is not installed' }
    Wait-Until { (Get-Service UsageControlGPU).Status -eq 'Running' } 'the gpu add-on runs'
    Wait-Until { Test-Path "$env:ProgramData\Usage Control\addons\gpu.json" } 'the gpu add-on wrote its report'
}

function Assert-KernelAddOn {
    $addOn = Get-Service UsageControlKernel -ErrorAction SilentlyContinue
    if (-not $addOn) { throw 'The kernel add-on is not installed' }
    Wait-Until { (Get-Service UsageControlKernel).Status -eq 'Running' } 'the kernel add-on runs'
    $report = "$env:ProgramData\Usage Control\addons\kernel.json"
    Wait-Until { (Test-Path $report) -and (Get-Content $report -Raw) -match '"context-switches"' } 'the kernel add-on wrote its report'
}

function Assert-PressureAddOn {
    $addOn = Get-Service UsageControlPressure -ErrorAction SilentlyContinue
    if (-not $addOn) { throw 'The pressure add-on is not installed' }
    Wait-Until { (Get-Service UsageControlPressure).Status -eq 'Running' } 'the pressure add-on runs'
    Wait-Until { Test-Path "$env:ProgramData\Usage Control\addons\pressure.json" } 'the pressure add-on wrote its report'
    Wait-Until { (Get-Content -Raw "$env:ProgramData\Usage Control\addons\pressure.json") -match '"cpu-queue"' } 'the pressure add-on read the processor queue'
}

# The runner has no Wi-Fi, and Windows Server may lack the WLAN API; the
# add-on then still runs and writes a report without values.
function Assert-WifiAddOn {
    $addOn = Get-Service UsageControlWifi -ErrorAction SilentlyContinue
    if (-not $addOn) { throw 'The Wi-Fi add-on is not installed' }
    Wait-Until { (Get-Service UsageControlWifi).Status -eq 'Running' } 'the Wi-Fi add-on runs'
    Wait-Until { Test-Path "$env:ProgramData\Usage Control\addons\wifi.json" } 'the Wi-Fi add-on wrote its report'
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

function Assert-PortsAddOn {
    if (-not (Get-Service UsageControlPorts -ErrorAction SilentlyContinue)) { throw 'The ports add-on is not installed' }
    Wait-Until { (Get-Service UsageControlPorts).Status -eq 'Running' } 'the ports add-on runs'
    $report = "$env:ProgramData\Usage Control\addons\ports.json"
    Wait-Until { (Test-Path $report) -and (Get-Content $report -Raw) -match '"id":"ports"' } 'the ports add-on wrote its ports'
}

# The smart add-on runs as LocalSystem, as SMART commands need an
# administrator. Its report may hold no disks on a virtual machine.
function Assert-SmartAddOn {
    $addOn = Get-CimInstance Win32_Service -Filter "Name = 'UsageControlSmart'"
    if (-not $addOn) { throw 'The smart add-on is not installed' }
    if ($addOn.StartName -ne 'LocalSystem') { throw "The smart add-on runs as $($addOn.StartName), not LocalSystem" }
    Wait-Until { (Get-Service UsageControlSmart).Status -eq 'Running' } 'the smart add-on runs'
    Wait-Until { Test-Path "$env:ProgramData\Usage Control\addons\smart.json" } 'the smart add-on wrote its report'
}

# The processes add-on lists ten processes by memory at once, and by CPU from
# its second read on, as the Local Service account sees them.
function Assert-ProcessesAddOn {
    $addOn = Get-Service UsageControlProcesses -ErrorAction SilentlyContinue
    if (-not $addOn) { throw 'The processes add-on is not installed' }
    Wait-Until { (Get-Service UsageControlProcesses).Status -eq 'Running' } 'the processes add-on runs'
    $report = "$env:ProgramData\Usage Control\addons\processes.json"
    Wait-Until { (Test-Path $report) -and (Get-Content $report -Raw) -like '*"processes-cpu"*' } 'the processes add-on reported the busiest processes'
    $groups = (Get-Content $report -Raw | ConvertFrom-Json).extras
    $memory = @($groups | Where-Object id -eq 'processes-memory')
    if ($memory.Count -ne 1 -or $memory[0].items.Count -ne 10) { throw "The processes add-on did not list ten processes by memory: $($groups | ConvertTo-Json -Depth 4)" }
}

Write-Host 'Installing with the website and the power, gpu, kernel, pressure, Wi-Fi, memory, ports, smart and processes add-ons on'
Invoke-Installer "/i `"$Msi`" PORT=8091 WEBSITE=1 DEVICE_NAME=Runner HUB_DEVICES=Pi=192.168.1.20:9393 RETENTION_DAYS=7 DISK_PATHS=$env:SystemDrive\ UPDATE_CHECK=false RESET_PASSWORD=true POWER=1 GPU=1 KERNEL=1 PRESSURE=1 WIFI=1 MEMORY=1 PORTS=1 SMART=1 PROCESSES=1"
$service = Get-Service UsageControl
if ($service.StartType -ne 'Automatic') { throw "The service starts $($service.StartType), not automatically" }
Assert-Website 8091
Assert-ServiceSetting DATA_ONLY 'false'
Assert-ServiceSetting HUB_DEVICES 'Pi=192.168.1.20:9393'
Assert-ServiceSetting DISK_PATHS "$env:SystemDrive\"
Assert-ServiceSetting UPDATE_CHECK 'false'
Assert-ServiceSetting RESET_PASSWORD 'true'
Assert-PowerAddOn
Assert-GpuAddOn
Assert-KernelAddOn
Assert-PressureAddOn
Assert-WifiAddOn
Assert-MemoryAddOn
Assert-PortsAddOn
Assert-SmartAddOn
Assert-ProcessesAddOn

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
Assert-ServiceSetting DISK_PATHS "$env:SystemDrive\"
Assert-ServiceSetting UPDATE_CHECK 'false'
Assert-ServiceSetting RESET_PASSWORD ''
Assert-PowerAddOn
Assert-GpuAddOn
Assert-KernelAddOn
Assert-PressureAddOn
Assert-WifiAddOn
Assert-MemoryAddOn
Assert-PortsAddOn
Assert-SmartAddOn
Assert-ProcessesAddOn

Write-Host 'A repair with RESET_PASSWORD=true restarts the service with it, and one without it turns it off'
$before = Get-ServiceProcessId
Invoke-Installer "/fm `"$NewerMsi`" RESET_PASSWORD=true"
Assert-ServiceSetting RESET_PASSWORD 'true'
Assert-Website 8091
if ((Get-ServiceProcessId) -eq $before) { throw 'The repair did not restart the service' }
Invoke-Installer "/fm `"$NewerMsi`""
Assert-ServiceSetting RESET_PASSWORD ''
Assert-ServiceSetting UPDATE_CHECK 'false'
Assert-Website 8091

Write-Host 'Uninstalling'
Invoke-Installer "/x `"$NewerMsi`""
if (Get-Service UsageControl -ErrorAction SilentlyContinue) { throw 'The service is still installed' }
if (Get-Service UsageControlPower -ErrorAction SilentlyContinue) { throw 'The power add-on is still installed' }
if (Get-Service UsageControlGPU -ErrorAction SilentlyContinue) { throw 'The gpu add-on is still installed' }
if (Get-Service UsageControlKernel -ErrorAction SilentlyContinue) { throw 'The kernel add-on is still installed' }
if (Get-Service UsageControlPressure -ErrorAction SilentlyContinue) { throw 'The pressure add-on is still installed' }
if (Get-Service UsageControlWifi -ErrorAction SilentlyContinue) { throw 'The Wi-Fi add-on is still installed' }
if (Get-Service UsageControlMemory -ErrorAction SilentlyContinue) { throw 'The memory add-on is still installed' }
if (Get-Service UsageControlPorts -ErrorAction SilentlyContinue) { throw 'The ports add-on is still installed' }
if (Get-Service UsageControlSmart -ErrorAction SilentlyContinue) { throw 'The smart add-on is still installed' }
if (Get-Service UsageControlProcesses -ErrorAction SilentlyContinue) { throw 'The processes add-on is still installed' }
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
Assert-ServiceSetting DATA_ONLY 'true'
if (Get-Service UsageControlPower -ErrorAction SilentlyContinue) { throw 'The power add-on was installed without POWER=1' }
if (Get-Service UsageControlGPU -ErrorAction SilentlyContinue) { throw 'The gpu add-on was installed without GPU=1' }
if (Get-Service UsageControlKernel -ErrorAction SilentlyContinue) { throw 'The kernel add-on was installed without KERNEL=1' }
if (Get-Service UsageControlPressure -ErrorAction SilentlyContinue) { throw 'The pressure add-on was installed without PRESSURE=1' }
if (Get-Service UsageControlWifi -ErrorAction SilentlyContinue) { throw 'The Wi-Fi add-on was installed without WIFI=1' }
if (Get-Service UsageControlMemory -ErrorAction SilentlyContinue) { throw 'The memory add-on was installed without MEMORY=1' }
if (Get-Service UsageControlPorts -ErrorAction SilentlyContinue) { throw 'The ports add-on was installed without PORTS=1' }
if (Get-Service UsageControlSmart -ErrorAction SilentlyContinue) { throw 'The smart add-on was installed without SMART=1' }
if (Get-Service UsageControlProcesses -ErrorAction SilentlyContinue) { throw 'The processes add-on was installed without PROCESSES=1' }
Invoke-Installer "/x `"$Msi`""

Write-Host 'An update with WEBSITE=0 turns the website and HUB_DEVICES off'
Invoke-Installer "/i `"$Msi`" WEBSITE=1 DEVICE_NAME=Runner HUB_DEVICES=Pi=192.168.1.20:9393"
Assert-Website 9393
Invoke-Installer "/i `"$NewerMsi`" WEBSITE=0"
Assert-ServiceSetting DATA_ONLY 'true'
Assert-ServiceSetting HUB_DEVICES ''
$null = Get-Answer 'http://127.0.0.1:9393/api/metrics'
$status = Get-StatusCode 'http://127.0.0.1:9393/'
if ($status -ne 404) { throw "The website answered $status after the update with WEBSITE=0; it should be off" }
Invoke-Installer "/x `"$NewerMsi`""

Write-Host 'The installer works'
