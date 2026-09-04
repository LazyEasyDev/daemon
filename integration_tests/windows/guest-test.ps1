param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("pre-reboot", "post-reboot", "cleanup")]
    [string]$Phase,
    [Parameter(Mandatory = $true)]
    [string]$ServiceName,
    [int]$Port = 18080
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$InstallDir = "C:\daemon-itest"
$Daemon = Join-Path $InstallDir "daemon.exe"
$App = Join-Path $InstallDir "test-app.exe"
$Fixture = Join-Path $InstallDir "relative-path-test.txt"
$RegistrationName = "lz_lz_$ServiceName"
$MetadataPath = Join-Path $env:ProgramData "daemon-util\services\$RegistrationName.json"
$ArtifactDir = Join-Path $InstallDir "artifacts"
$BootEvents = Join-Path $InstallDir "boot-events.jsonl"
$RestartEvents = Join-Path $InstallDir "restart-events.jsonl"

function Write-TestLog([string]$Message) {
    Write-Host "[windows-itest] $Message"
}

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) {
        throw $Message
    }
}

function Invoke-Daemon([string[]]$Arguments) {
    & $Daemon @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "daemon command failed ($LASTEXITCODE): $($Arguments -join ' ')"
    }
}

function Get-AppResponse {
    Invoke-RestMethod -UseBasicParsing -Uri "http://127.0.0.1:$Port/" -TimeoutSec 5
}

function Wait-App([int]$TimeoutSeconds = 90) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $response = Get-AppResponse
            if ($response.executable -eq $App -and
                $response.config.message -eq "hello windows server 2019" -and
                [int]$response.config.count -eq 7 -and
                $response.file_content -match "daemon-util relative path test passed") {
                return $response
            }
        } catch {
        }
        Start-Sleep -Seconds 2
    }
    throw "application HTTP endpoint did not become ready"
}

function Wait-NewPid([int]$OldPid, [int]$TimeoutSeconds = 120) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $response = Get-AppResponse
            if ([int]$response.pid -ne $OldPid) {
                return [int]$response.pid
            }
        } catch {
        }
        Start-Sleep -Seconds 2
    }
    throw "application did not restart from PID $OldPid"
}

function Get-EventCount([string]$Path, [string]$EventName) {
    if (-not (Test-Path $Path)) {
        return 0
    }
    return @(
        Get-Content $Path | ForEach-Object {
            try { $_ | ConvertFrom-Json } catch { $null }
        } | Where-Object { $_.event -eq $EventName }
    ).Count
}

function Assert-NoAppProcesses {
    $matches = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -eq $App })
    Assert-True ($matches.Count -eq 0) "test application process leaked after cleanup"
}

function Remove-TestService {
    $service = Get-Service -Name $RegistrationName -ErrorAction SilentlyContinue
    if ($null -ne $service) {
        if ($service.Status -ne "Stopped") {
            try { Invoke-Daemon @("stop", $ServiceName) } catch { Stop-Service -Name $RegistrationName -Force -ErrorAction SilentlyContinue }
        }
        try { Invoke-Daemon @("remove", $ServiceName) } catch { & sc.exe delete $RegistrationName | Out-Null }
        $deadline = (Get-Date).AddSeconds(30)
        while ((Get-Date) -lt $deadline -and (Get-Service -Name $RegistrationName -ErrorAction SilentlyContinue)) {
            Start-Sleep -Seconds 1
        }
    }
    Remove-Item $MetadataPath -Force -ErrorAction SilentlyContinue
}

function Install-TestService([string]$Events, [string[]]$ExtraArguments = @()) {
    Remove-Item $Events -Force -ErrorAction SilentlyContinue
    $arguments = @(
        "install", "--stop-timeout", "5s", "--ignore-warnings",
        $ServiceName, $App,
        "--enabled=true",
        "--message", "hello windows server 2019",
        "--count", "7",
        "--port", "$Port",
        "--file-path", "relative-path-test.txt",
        "--event-path", $Events
    ) + $ExtraArguments
    Invoke-Daemon $arguments
}

function Verify-ServiceDefinition {
    $service = Get-CimInstance Win32_Service -Filter "Name='$RegistrationName'"
    Assert-True ($null -ne $service) "Windows SCM service was not installed"
    Assert-True ($service.StartMode -eq "Auto") "service start mode is not automatic"
    Assert-True ($service.PathName -match "run-windows-service") "SCM ImagePath does not use the daemon wrapper"
    Assert-True ($service.PathName -match [regex]::Escape($App)) "SCM ImagePath does not contain the application path"
    Assert-True (Test-Path $MetadataPath) "service metadata was not written"
    $failure = (& sc.exe qfailure $RegistrationName 2>&1 | Out-String)
    Assert-True ($failure -match "RESTART") "SCM recovery actions do not restart the service"
}

function Verify-ManagementCommands {
    $status = (& $Daemon status $ServiceName 2>&1 | Out-String)
    Assert-True ($LASTEXITCODE -eq 0 -and $status -match "running") "daemon status does not report running"
    $list = (& $Daemon list -l 2>&1 | Out-String)
    Assert-True ($LASTEXITCODE -eq 0 -and $list -match [regex]::Escape($ServiceName)) "daemon list omits the service"
    Assert-True ($list -match "hello windows server 2019") "daemon list omits application arguments"
    Assert-True ((Get-Service -Name $RegistrationName).Status -eq "Running") "SCM does not report Running"
}

function Save-Artifacts([string]$Label) {
    New-Item -ItemType Directory -Path $ArtifactDir -Force | Out-Null
    Get-CimInstance Win32_OperatingSystem | Format-List * | Out-File (Join-Path $ArtifactDir "$Label-os.txt")
    Get-CimInstance Win32_Service -Filter "Name='$RegistrationName'" | Format-List * | Out-File (Join-Path $ArtifactDir "$Label-service.txt")
    (& sc.exe qc $RegistrationName 2>&1 | Out-String) | Out-File (Join-Path $ArtifactDir "$Label-sc-qc.txt")
    (& sc.exe qfailure $RegistrationName 2>&1 | Out-String) | Out-File (Join-Path $ArtifactDir "$Label-sc-qfailure.txt")
    Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -eq $App } | ConvertTo-Json -Depth 4 | Out-File (Join-Path $ArtifactDir "$Label-processes.json")
    Copy-Item $BootEvents, $RestartEvents -Destination $ArtifactDir -Force -ErrorAction SilentlyContinue
}

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
Assert-True ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) "guest test requires Administrator"
Assert-True (Test-Path $Daemon) "daemon executable is missing"
Assert-True (Test-Path $App) "test application is missing"
Assert-True (Test-Path $Fixture) "relative-path fixture is missing"
New-Item -ItemType Directory -Path $ArtifactDir -Force | Out-Null

switch ($Phase) {
    "pre-reboot" {
        Write-TestLog "installing automatic service for reboot test"
        Remove-TestService
        Install-TestService $BootEvents @("--stop_delay", "1s")
        Verify-ServiceDefinition
        Invoke-Daemon @("start", $ServiceName)
        $response = Wait-App
        Verify-ManagementCommands
        $response | ConvertTo-Json -Depth 8 | Out-File (Join-Path $ArtifactDir "pre-reboot-http.json")
        Save-Artifacts "pre-reboot"
        Write-TestLog "pre-reboot app checks passed with PID $($response.pid)"
    }
    "post-reboot" {
        Write-TestLog "verifying reboot auto-start"
        $response = Wait-App
        Assert-True ((Get-EventCount $BootEvents "started") -ge 2) "application did not auto-start after reboot"
        Verify-ManagementCommands
        $response | ConvertTo-Json -Depth 8 | Out-File (Join-Path $ArtifactDir "post-reboot-http.json")

        $oldPid = [int]$response.pid
        Invoke-Daemon @("restart", $ServiceName)
        $newPid = Wait-NewPid $oldPid
        Write-TestLog "explicit restart changed PID $oldPid to $newPid"

        $stopStarted = Get-Date
        Invoke-Daemon @("stop", $ServiceName)
        $elapsed = ((Get-Date) - $stopStarted).TotalSeconds
        Assert-True ($elapsed -ge 1 -and $elapsed -lt 15) "graceful stop duration was $elapsed seconds"
        Assert-True ((Get-EventCount $BootEvents "stopped") -ge 1) "graceful stop event is missing"
        Invoke-Daemon @("remove", $ServiceName)
        Assert-True (-not (Get-Service -Name $RegistrationName -ErrorAction SilentlyContinue)) "service remains after removal"

        Write-TestLog "verifying configured failure and hard-crash SCM recovery"
        Install-TestService $RestartEvents @("--stop-after", "20s")
        Verify-ServiceDefinition
        Invoke-Daemon @("start", $ServiceName)
        $first = Wait-App
        $failurePid = Wait-NewPid ([int]$first.pid) 120
        Assert-True ((Get-EventCount $RestartEvents "failure") -ge 1) "configured failure event is missing"
        Assert-True ((Get-EventCount $RestartEvents "started") -ge 2) "configured failure did not restart the app"
        Write-TestLog "configured failure changed PID $($first.pid) to $failurePid"

        Stop-Process -Id $failurePid -Force
        $hardPid = Wait-NewPid $failurePid 120
        Assert-True ((Get-EventCount $RestartEvents "started") -ge 3) "hard crash did not restart the app"
        Verify-ManagementCommands
        Write-TestLog "hard crash changed PID $failurePid to $hardPid"

        Invoke-Daemon @("stop", $ServiceName)
        Invoke-Daemon @("start", $ServiceName)
        $final = Wait-App
        Write-TestLog "stop/start lifecycle restored PID $($final.pid)"
        Invoke-Daemon @("stop", $ServiceName)
        Invoke-Daemon @("remove", $ServiceName)
        Assert-True (-not (Get-Service -Name $RegistrationName -ErrorAction SilentlyContinue)) "service remains after final removal"
        Assert-True (-not (Test-Path $MetadataPath)) "metadata remains after final removal"
        Assert-NoAppProcesses
        Save-Artifacts "success"
        Write-TestLog "all Windows application-level tests passed"
    }
    "cleanup" {
        Remove-TestService
        Assert-NoAppProcesses
    }
}
