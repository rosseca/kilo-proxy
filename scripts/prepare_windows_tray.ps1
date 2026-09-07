param([Parameter(Mandatory = $true)][int]$SessionId)
$ErrorActionPreference = 'Stop'

# This file is a CI fixture, never an application startup path. Repeat all
# environment/session guards at the boundary that can terminate a process.
if ($env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted' -or
    $env:RUNNER_OS -ne 'Windows' -or $env:RUNNER_ARCH -ne 'ARM64' -or
    $SessionId -le 0 -or [Diagnostics.Process]::GetCurrentProcess().SessionId -ne $SessionId) {
    throw 'OOBE recovery is restricted to the current hosted Windows ARM64 runner session.'
}
$allowed = @('msoobe', 'CloudExperienceHost', 'CloudExperienceHostBroker', 'WWAHost')
$blockers = @(Get-Process -Name $allowed -ErrorAction SilentlyContinue |
    Where-Object { $_.SessionId -eq $SessionId })
if ($blockers.Count -eq 0) {
    throw 'No allowlisted OOBE host is active in the current runner session; shell unchanged.'
}
foreach ($process in $blockers) {
    Write-Output "Stopping CI OOBE host: $($process.ProcessName)"
    Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
}
$explorers = @(Get-Process -Name explorer -ErrorAction SilentlyContinue |
    Where-Object { $_.SessionId -eq $SessionId })
foreach ($process in $explorers) {
    Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
}
Start-Sleep -Seconds 3
Start-Process -FilePath (Join-Path $env:WINDIR 'explorer.exe')
# Explorer creates its notification service asynchronously. The Python caller
# enforces a 45-second process timeout and then requires a successful API probe.
Start-Sleep -Seconds 15
