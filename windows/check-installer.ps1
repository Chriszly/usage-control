# Checks the Windows installer on a Windows machine, as an administrator:
# checks it refuses an UPDATE_CHECK the service cannot read, installs it
# with the website and the power, gpu, kernel, pressure, Wi-Fi, memory,
# ports, smart and processes add-ons on, checks the tray icon pauses,
# resumes and stops the service, updates it to a newer bugfix without
# options and checks the options and the add-ons were kept, except
# RESET_PASSWORD, and the tray icon was closed for the update, checks the
# older bugfix then refuses to install over it, repairs it with
# RESET_PASSWORD=true and again without, as the docs say to, which restarts
# the service and leaves the tray icon running, checks a repair refuses an
# UPDATE_CHECK the service cannot read and takes one it can, switches the
# website off and on again with repairs, checks a repair refuses an add-on
# option other than 0 or 1, removes the power add-on and adds it again with
# repairs and checks a repair without options and a full repair keep the
# add-ons, the full one closing the tray icon, checks a bad remembered
# UPDATE_CHECK is refused with the repair that fixes it, which does, but does
# not hold up the uninstall, uninstalls it, then installs it with the defaults
# and checks it only serves the usage data, and last checks that an update
# with WEBSITE=0 turns the website off and that one with a space clears
# DISK_PATHS, UPDATE_CHECK and PORT.
#
#   pwsh windows/check-installer.ps1 -Msi usage-control-1.2.3-x64.msi -NewerMsi usage-control-1.2.3a-x64.msi
param(
    [Parameter(Mandatory)] [string] $Msi,
    # The same installer with the next bugfix version, to check an update.
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

# The versions of Usage Control that Windows lists as installed.
function Get-InstalledVersions {
    @(Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*' |
        Where-Object { $_.PSObject.Properties['DisplayName'] -and $_.DisplayName -eq 'Usage Control' } |
        ForEach-Object { $_.DisplayVersion }) -join ', '
}

# Runs the installer and checks it refuses with a message that holds each of
# Messages, as a launch condition that is not met does, and changes nothing.
function Assert-InstallerRefuses([string] $Arguments, [string[]] $Messages) {
    $before = Get-InstalledVersions
    $process = Start-Process msiexec.exe -ArgumentList "$Arguments /qn /l*v msiexec.log" -Wait -PassThru
    # 1603 is a failed install. Without the zeros the log reads the same
    # whether it was written as ANSI or as UTF-16.
    $log = (Get-Content msiexec.log -Raw) -replace "`0", ''
    $missing = @($Messages | Where-Object { -not $log.Contains($_) })
    if ($process.ExitCode -ne 1603 -or $missing.Count -gt 0) {
        Get-Content msiexec.log -Tail 80
        throw "msiexec $Arguments exited with $($process.ExitCode); it should refuse with '$($Messages -join "' and '")' and 1603"
    }
    $after = Get-InstalledVersions
    if ($after -ne $before) { throw "msiexec $Arguments changed the installed version from '$before' to '$after'" }
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

Write-Host 'An UPDATE_CHECK other than true or false is refused, naming the option'
Assert-InstallerRefuses "/i `"$Msi`" UPDATE_CHECK=no" 'UPDATE_CHECK must be true or false.', ".msi`" UPDATE_CHECK=true"
if (Get-Service UsageControl -ErrorAction SilentlyContinue) { throw 'The refused install installed the service' }

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

Write-Host 'The older bugfix refuses to install over the newer one'
Assert-InstallerRefuses "/i `"$Msi`"" 'A newer bugfix of this version of Usage Control is already installed.'
Assert-Website 8091

Write-Host 'A repair with RESET_PASSWORD=true restarts the service with it, and one without it turns it off'
Start-Tray
$before = Get-ServiceProcessId
Invoke-Installer "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m RESET_PASSWORD=true"
Assert-ServiceSetting RESET_PASSWORD 'true'
Assert-Website 8091
if ((Get-ServiceProcessId) -eq $before) { throw 'The repair did not restart the service' }
if (-not (Get-Process usage-control-tray -ErrorAction SilentlyContinue)) { throw 'The repair closed the tray icon' }
Invoke-Installer "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m"
Assert-ServiceSetting RESET_PASSWORD ''
Assert-ServiceSetting UPDATE_CHECK 'false'
Assert-Website 8091

Write-Host 'A repair refuses an UPDATE_CHECK the service cannot read and takes one it can'
Assert-InstallerRefuses "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m UPDATE_CHECK=no" 'UPDATE_CHECK must be true or false.'
Assert-ServiceSetting UPDATE_CHECK 'false'
Invoke-Installer "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m UPDATE_CHECK=F"
Assert-ServiceSetting UPDATE_CHECK 'F'
Assert-Website 8091

Write-Host 'A repair with WEBSITE=0 turns the website off, and one with WEBSITE=1 on again'
Invoke-Installer "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m WEBSITE=0"
Assert-ServiceSetting DATA_ONLY 'true'
$null = Get-Answer 'http://127.0.0.1:8091/api/metrics'
$status = Get-StatusCode 'http://127.0.0.1:8091/'
if ($status -ne 404) { throw "The website answered $status after the repair with WEBSITE=0; it should be off" }
Invoke-Installer "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m WEBSITE=1"
Assert-ServiceSetting DATA_ONLY 'false'
Assert-Website 8091

Write-Host 'A repair refuses an add-on option other than 0 or 1, naming the repair that gives it'
Assert-InstallerRefuses "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m POWER=true" 'POWER must be 0 or 1.', ".msi`" REINSTALL=ALL REINSTALLMODE=m POWER=1"
if ((Get-ItemPropertyValue 'HKLM:\SOFTWARE\Usage Control' POWER) -ne '1') { throw 'The refused repair changed the remembered POWER' }
Assert-PowerAddOn

Write-Host 'A repair with POWER=0 removes the power add-on and keeps the others, and one with POWER=1 adds it again'
# The reports the add-ons left behind, so the checks below see new ones.
Remove-Item "$env:ProgramData\Usage Control\addons\*.json" -ErrorAction SilentlyContinue
Invoke-Installer "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m POWER=0"
if (Get-Service UsageControlPower -ErrorAction SilentlyContinue) { throw 'The repair with POWER=0 left the power add-on installed' }
if (Test-Path "$env:ProgramFiles\Usage Control\usage-control-power.exe") { throw 'The repair with POWER=0 left usage-control-power.exe' }
if ((Get-ItemPropertyValue 'HKLM:\SOFTWARE\Usage Control' POWER) -ne '0') { throw 'The repair did not remember POWER=0' }
Assert-GpuAddOn
Assert-KernelAddOn
Assert-PressureAddOn
Assert-WifiAddOn
Assert-MemoryAddOn
Assert-PortsAddOn
Assert-SmartAddOn
Assert-ProcessesAddOn
Assert-Website 8091
# The report the add-on left behind, so the check below sees a new one.
Remove-Item "$env:ProgramData\Usage Control\addons\power.json" -ErrorAction SilentlyContinue
Invoke-Installer "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m POWER=1"
Assert-PowerAddOn
if ((Get-ItemPropertyValue 'HKLM:\SOFTWARE\Usage Control' POWER) -ne '1') { throw 'The repair did not remember POWER=1' }
Write-Host 'A repair without add-on options keeps the add-ons'
Invoke-Installer "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m"
Assert-PowerAddOn
Assert-GpuAddOn
Assert-Website 8091

Write-Host 'A full repair, which replaces every file, keeps the add-ons and closes the tray icon'
Start-Tray
Remove-Item "$env:ProgramData\Usage Control\addons\*.json" -ErrorAction SilentlyContinue
Invoke-Installer "/fa `"$NewerMsi`""
if (Get-Process usage-control-tray -ErrorAction SilentlyContinue) { throw 'The full repair left the tray icon running' }
Assert-PowerAddOn
Assert-GpuAddOn
Assert-KernelAddOn
Assert-PressureAddOn
Assert-WifiAddOn
Assert-MemoryAddOn
Assert-PortsAddOn
Assert-SmartAddOn
Assert-ProcessesAddOn
Assert-Website 8091

Write-Host 'A bad remembered UPDATE_CHECK names the repair that fixes it, which does, and does not hold up an uninstall'
Set-ItemProperty 'HKLM:\SOFTWARE\Usage Control' -Name UPDATE_CHECK -Value 'no'
Assert-InstallerRefuses "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m" ".msi`" REINSTALL=ALL REINSTALLMODE=m UPDATE_CHECK=true"
Invoke-Installer "/i `"$NewerMsi`" REINSTALL=ALL REINSTALLMODE=m UPDATE_CHECK=true"
Assert-ServiceSetting UPDATE_CHECK 'true'
if ((Get-ItemPropertyValue 'HKLM:\SOFTWARE\Usage Control' UPDATE_CHECK) -ne 'true') { throw 'The repair did not rewrite the remembered UPDATE_CHECK' }
Set-ItemProperty 'HKLM:\SOFTWARE\Usage Control' -Name UPDATE_CHECK -Value 'no'

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

Write-Host 'An update with WEBSITE=0 turns the website and HUB_DEVICES off, and a space clears DISK_PATHS, UPDATE_CHECK and PORT'
Invoke-Installer "/i `"$Msi`" PORT=8092 WEBSITE=1 DEVICE_NAME=Runner HUB_DEVICES=Pi=192.168.1.20:9393 DISK_PATHS=$env:SystemDrive\ UPDATE_CHECK=false"
Assert-Website 8092
# DATA_ONLY=false is what the setup wizard used to pass on after the
# remembered WEBSITE=1, which kept the website on. An empty option such as
# DISK_PATHS="" would count as not given, so a space clears it; PORT then
# goes back to 9393.
Invoke-Installer "/i `"$NewerMsi`" WEBSITE=0 DATA_ONLY=false DISK_PATHS=`" `" UPDATE_CHECK=`" `" PORT=`" `""
Assert-ServiceSetting DATA_ONLY 'true'
Assert-ServiceSetting HUB_DEVICES ''
Assert-ServiceSetting DISK_PATHS ' '
Assert-ServiceSetting UPDATE_CHECK ' '
Assert-ServiceSetting LISTEN_ADDR ':9393'
Assert-FirewallPort 9393
$null = Get-Answer 'http://127.0.0.1:9393/api/metrics'
$status = Get-StatusCode 'http://127.0.0.1:9393/'
if ($status -ne 404) { throw "The website answered $status after the update with WEBSITE=0; it should be off" }
Invoke-Installer "/x `"$NewerMsi`""

Write-Host 'The installer works'
