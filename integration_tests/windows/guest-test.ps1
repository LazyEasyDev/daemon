param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("pre-reboot", "hot-replacement", "post-reboot", "cleanup")]
    [string]$Phase,
    [Parameter(Mandatory = $true)]
    [string]$ServiceName,
    [int]$Port = 18080,
    [Parameter(Mandatory = $true)]
    [string]$ExpectedMessage
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
$ForcedEvents = Join-Path $InstallDir "forced-events.jsonl"
$ChildPIDPath = Join-Path $InstallDir "child.pid"

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
                $response.config.message -eq $ExpectedMessage -and
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

function Wait-AppProcessGone([int]$ProcessId, [int]$TimeoutSeconds = 60) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $process = Get-CimInstance Win32_Process -Filter "ProcessId=$ProcessId" -ErrorAction SilentlyContinue
        if ($null -eq $process) {
            return
        }
        Start-Sleep -Seconds 1
    }
    throw "application PID $ProcessId did not exit"
}

function Assert-SharingViolation([object]$ErrorRecord) {
    $exception = $ErrorRecord.Exception
    if ($null -ne $exception.InnerException) {
        $exception = $exception.InnerException
    }
    $nativeCode = $exception.HResult -band 0xFFFF
    $message = $ErrorRecord | Out-String
    Assert-True (
        $nativeCode -eq 32 -or
        $message -match "sharing violation|used by another process|0x80070020"
    ) "running executable replacement failed unexpectedly: $message"
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

function Wait-AuxiliaryPid([string]$Path, [int]$TimeoutSeconds = 60) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-Path $Path) {
            $value = (Get-Content $Path -Raw).Trim()
            if ($value -match '^\d+$' -and (Get-Process -Id ([int]$value) -ErrorAction SilentlyContinue)) {
                return [int]$value
            }
        }
        Start-Sleep -Seconds 1
    }
    throw "interpreted application did not write a live PID to $Path"
}

function Remove-AuxiliaryService([string]$LogicalName) {
    $registered = "lz_lz_$LogicalName"
    $metadata = Join-Path $env:ProgramData "daemon-util\services\$registered.json"
    $service = Get-Service -Name $registered -ErrorAction SilentlyContinue
    if ($null -ne $service) {
        if ($service.Status -ne 'Stopped') {
            & $Daemon stop $LogicalName | Out-Null
        }
        & $Daemon remove $LogicalName | Out-Null
        $deadline = (Get-Date).AddSeconds(30)
        while ((Get-Date) -lt $deadline -and (Get-Service -Name $registered -ErrorAction SilentlyContinue)) {
            Start-Sleep -Seconds 1
        }
    }
    Assert-True (-not (Get-Service -Name $registered -ErrorAction SilentlyContinue)) "auxiliary service remains: $registered"
    Assert-True (-not (Test-Path $metadata)) "auxiliary metadata remains: $registered"
}

function Verify-AdditionalApplications {
    Write-TestLog 'verifying symlinked native, PowerShell, optional Python, and rejected direct-script applications'
    $linkName = "${ServiceName}symlinkapp"
    $linkRegistration = "lz_lz_$linkName"
    $appLink = Join-Path $InstallDir 'test-app-symlink.exe'
    Remove-Item $appLink -Force -ErrorAction SilentlyContinue
    New-Item -ItemType SymbolicLink -Path $appLink -Target $App -Force | Out-Null
    Invoke-Daemon @(
        'install', '--ignore-warnings', $linkName, $appLink,
        '--enabled=true', '--message', $ExpectedMessage, '--count', '7', '--port', "$Port",
        '--file-path', 'relative-path-test.txt', '--event-path', (Join-Path $ArtifactDir 'symlink-application.events.jsonl')
    )
    $linkService = Get-CimInstance Win32_Service -Filter "Name='$linkRegistration'"
    Assert-True ($linkService.PathName -match [regex]::Escape($App)) 'symlink service did not store the resolved PE path'
    Invoke-Daemon @('start', $linkName)
    $linkResponse = Wait-App
    $linkPid = [int]$linkResponse.pid
    Invoke-Daemon @('stop', $linkName)
    Wait-AppProcessGone $linkPid
    Remove-AuxiliaryService $linkName
    Remove-Item $appLink -Force -ErrorAction SilentlyContinue

    $powershellScript = Join-Path $InstallDir 'powershell-application.ps1'
    $powershellState = Join-Path $ArtifactDir 'powershell-application'
    @'
param([string]$State)
Set-Content -Path "$State.pid" -Value $PID
Add-Content -Path "$State.events" -Value 'started'
while ($true) { Start-Sleep -Seconds 1 }
'@ | Set-Content -Path $powershellScript -Encoding UTF8

    $rejectedName = "${ServiceName}directscript"
    $savedErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $rejection = (& $Daemon install --ignore-warnings $rejectedName $powershellScript 2>&1 | Out-String)
        $rejectionStatus = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedErrorActionPreference
    }
    $rejection | Out-File (Join-Path $ArtifactDir 'direct-script-rejection.txt')
    Assert-True ($rejectionStatus -ne 0) 'direct PowerShell script was accepted as a native executable'
    Remove-AuxiliaryService $rejectedName

    $powershellName = "${ServiceName}powershellapp"
    $powershellExe = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    Remove-Item "$powershellState.pid", "$powershellState.events" -Force -ErrorAction SilentlyContinue
    Invoke-Daemon @('install', '--ignore-warnings', $powershellName, $powershellExe,
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $powershellScript, $powershellState)
    Invoke-Daemon @('start', $powershellName)
    $powershellPid = Wait-AuxiliaryPid "$powershellState.pid"
    $powershellList = (& $Daemon list -l 2>&1 | Out-String)
    Assert-True ($powershellList -match [regex]::Escape($powershellScript)) 'PowerShell application arguments are absent from list -l'
    Invoke-Daemon @('stop', $powershellName)
    Wait-AppProcessGone $powershellPid
    Remove-AuxiliaryService $powershellName

    $pythonCommand = Get-Command python.exe -ErrorAction SilentlyContinue
    $pythonExe = $null
    if ($null -ne $pythonCommand) {
        $pythonReported = (& $pythonCommand.Source -c 'import sys; print(sys.executable)' 2>$null | Select-Object -Last 1)
        if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($pythonReported) -and (Test-Path $pythonReported)) {
            $pythonExe = (Get-Item $pythonReported).FullName
        }
    }
    if ($null -eq $pythonExe) {
        'SKIP: python.exe is not installed in this guest' | Out-File (Join-Path $ArtifactDir 'python-application-skip.txt')
    } else {
        $pythonScript = Join-Path $InstallDir 'python-application.py'
        $pythonState = Join-Path $ArtifactDir 'python-application'
        @'
import os, sys, time
state = sys.argv[1]
with open(state + '.pid', 'w', encoding='utf-8') as output:
    output.write(str(os.getpid()))
with open(state + '.events', 'a', encoding='utf-8') as output:
    output.write('started\n')
while True:
    time.sleep(1)
'@ | Set-Content -Path $pythonScript -Encoding UTF8
        $pythonName = "${ServiceName}pythonapp"
        Remove-Item "$pythonState.pid", "$pythonState.events" -Force -ErrorAction SilentlyContinue
        Invoke-Daemon @('install', '--ignore-warnings', $pythonName, $pythonExe, $pythonScript, $pythonState)
        Invoke-Daemon @('start', $pythonName)
        $pythonPid = Wait-AuxiliaryPid "$pythonState.pid"
        Invoke-Daemon @('stop', $pythonName)
        Wait-AppProcessGone $pythonPid
        Remove-AuxiliaryService $pythonName
    }
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

function Install-TestService(
    [string]$Events,
    [string[]]$ExtraArguments = @(),
    [string]$StopTimeout = "5s"
) {
    Remove-Item $Events -Force -ErrorAction SilentlyContinue
    Remove-Item $ChildPIDPath -Force -ErrorAction SilentlyContinue
    $arguments = @(
        "install", "--stop-timeout", $StopTimeout, "--ignore-warnings",
        $ServiceName, $App,
        "--enabled=true",
        "--message", $ExpectedMessage,
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
    $list = (& $Daemon list 2>&1 | Out-String)
    Assert-True ($LASTEXITCODE -eq 0 -and $list -match [regex]::Escape($ServiceName)) "daemon list omits the service"
    $longList = (& $Daemon list -l 2>&1 | Out-String)
    Assert-True ($LASTEXITCODE -eq 0 -and $longList -match [regex]::Escape($ServiceName)) "daemon long list omits the service"
    Assert-True ($longList -match [regex]::Escape($ExpectedMessage)) "daemon long list omits application arguments"
    Assert-True ((Get-Service -Name $RegistrationName).Status -eq "Running") "SCM does not report Running"
}

function Save-Artifacts([string]$Label) {
    New-Item -ItemType Directory -Path $ArtifactDir -Force | Out-Null
    Get-CimInstance Win32_OperatingSystem | Format-List * | Out-File (Join-Path $ArtifactDir "$Label-os.txt")
    Get-CimInstance Win32_Service -Filter "Name='$RegistrationName'" | Format-List * | Out-File (Join-Path $ArtifactDir "$Label-service.txt")
    (& sc.exe qc $RegistrationName 2>&1 | Out-String) | Out-File (Join-Path $ArtifactDir "$Label-sc-qc.txt")
    (& sc.exe qfailure $RegistrationName 2>&1 | Out-String) | Out-File (Join-Path $ArtifactDir "$Label-sc-qfailure.txt")
    Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -eq $App } | ConvertTo-Json -Depth 4 | Out-File (Join-Path $ArtifactDir "$Label-processes.json")
    Copy-Item $BootEvents, $RestartEvents, $ForcedEvents -Destination $ArtifactDir -Force -ErrorAction SilentlyContinue
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
    "hot-replacement" {
        Write-TestLog "testing replacement of the running application image"
        $Replacement = Join-Path $InstallDir "test-app-replacement.exe"
        $Backup = Join-Path $InstallDir "test-app-hot-replacement-backup.exe"
        Assert-True (Test-Path $Replacement) "replacement executable is missing"
        Remove-Item $Backup -Force -ErrorAction SilentlyContinue

        $response = Wait-App
        $oldPid = [int]$response.pid
        $originalHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $App).Hash
        $originalTime = (Get-Item -LiteralPath $App).LastWriteTimeUtc
        $replacementHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $Replacement).Hash
        Assert-True ($replacementHash -ne $originalHash) "replacement executable is not distinguishable from the original"

        $runningReplaceSucceeded = $false
        $runningReplaceError = $null
        try {
            [System.IO.File]::Replace($Replacement, $App, $Backup, $true)
            $runningReplaceSucceeded = $true
        } catch {
            $runningReplaceError = $_
        }

        if ($runningReplaceSucceeded) {
            $targetHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $App).Hash
            Assert-True ($targetHash -eq $replacementHash) "live replacement target hash does not match the candidate"
            $result = "live-replacement-succeeded"
        } else {
            Assert-SharingViolation $runningReplaceError
            $targetHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $App).Hash
            $targetTime = (Get-Item -LiteralPath $App).LastWriteTimeUtc
            Assert-True ($targetHash -eq $originalHash) "target changed after the expected sharing violation"
            Assert-True ($targetTime -eq $originalTime) "target timestamp changed after the expected sharing violation"
            $result = "expected-sharing-violation"
        }

        $runningResponse = Wait-App
        Assert-True ([int]$runningResponse.pid -eq $oldPid) "application PID changed during running-image replacement"
        Verify-ManagementCommands

        Invoke-Daemon @("stop", $ServiceName)
        Wait-AppProcessGone $oldPid
        if (-not $runningReplaceSucceeded) {
            Remove-Item $Backup -Force -ErrorAction SilentlyContinue
            [System.IO.File]::Replace($Replacement, $App, $Backup, $true)
        }
        $installedHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $App).Hash
        Assert-True ($installedHash -eq $replacementHash) "target executable does not contain the replacement after stop"
        Remove-Item $Backup -Force -ErrorAction SilentlyContinue

        Invoke-Daemon @("start", $ServiceName)
        $newResponse = Wait-App
        $newPid = [int]$newResponse.pid
        Assert-True ($newPid -ne $oldPid) "replacement start reused old PID $oldPid"
        Verify-ManagementCommands

        [pscustomobject]@{
            running_operation = $result
            original_hash = $originalHash
            replacement_hash = $replacementHash
            installed_hash = $installedHash
            old_pid = $oldPid
            new_pid = $newPid
        } | ConvertTo-Json -Depth 5 | Out-File (Join-Path $ArtifactDir "hot-replacement.json")
        $newResponse | ConvertTo-Json -Depth 8 | Out-File (Join-Path $ArtifactDir "hot-replacement-http.json")
        Save-Artifacts "hot-replacement"
        Write-TestLog "hot replacement result '$result'; replacement start changed PID $oldPid to $newPid"
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

        Write-TestLog "verifying forced timeout and Job Object child cleanup"
        Install-TestService -Events $ForcedEvents -ExtraArguments @(
            "--stop_delay", "30s",
            "--spawn-child=true",
            "--child-pid-path", $ChildPIDPath
        ) -StopTimeout "2s"
        Verify-ServiceDefinition
        Invoke-Daemon @("start", $ServiceName)
        $forced = Wait-App
        $forcedPid = [int]$forced.pid
        $childPid = [int]$forced.child_pid
        Assert-True ($childPid -gt 0) "spawned child PID is missing from the application response"
        Assert-True (Test-Path $ChildPIDPath) "spawned child PID file is missing"
        Assert-True ([int](Get-Content $ChildPIDPath -Raw) -eq $childPid) "spawned child PID file does not match the application response"

        $stopStarted = Get-Date
        Invoke-Daemon @("stop", $ServiceName)
        $elapsed = ((Get-Date) - $stopStarted).TotalSeconds
        Assert-True ($elapsed -ge 2 -and $elapsed -lt 20) "forced stop duration was $elapsed seconds"
        Assert-True ((Get-EventCount $ForcedEvents "signal") -ge 1) "forced-stop signal event is missing"
        Assert-True ((Get-EventCount $ForcedEvents "stopped") -eq 0) "application completed gracefully despite forced termination"
        Wait-AppProcessGone $forcedPid
        Wait-AppProcessGone $childPid
        Assert-NoAppProcesses
        Assert-True ((Get-Service -Name $RegistrationName).Status -eq "Stopped") "SCM does not report Stopped after forced termination"
        Save-Artifacts "forced-stop"
        Invoke-Daemon @("remove", $ServiceName)
        Assert-True (-not (Get-Service -Name $RegistrationName -ErrorAction SilentlyContinue)) "service remains after forced-stop removal"
        Assert-True (-not (Test-Path $MetadataPath)) "metadata remains after forced-stop removal"
        Verify-AdditionalApplications
        Save-Artifacts "success"
        Write-TestLog "all Windows application-level tests passed"
    }
    "cleanup" {
        Remove-TestService
        foreach ($suffix in @('symlinkapp', 'directscript', 'powershellapp', 'pythonapp')) {
            Remove-AuxiliaryService "${ServiceName}$suffix"
        }
        Assert-NoAppProcesses
    }
}
