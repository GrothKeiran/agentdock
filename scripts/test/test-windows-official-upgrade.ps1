[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string] $LegacySetup,
    [Parameter(Mandatory = $true)][string] $TargetSetup,
    [Parameter(Mandatory = $true)][string] $TestRoot,
    [switch] $ExpectFailure,
    [ValidateSet('standard', 'elevated')][string] $Privilege = 'standard',
    [string] $BrokenSetup = '',
    [string] $RecoveryBinary = ''
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Join-Path $TestRoot 'installed'
New-Item -ItemType Directory -Path $TestRoot -Force | Out-Null
function Invoke-TestSetup([string] $Path, [string] $Label) {
    $log = Join-Path $TestRoot "$Label.log"
    $process = Start-Process -FilePath $Path -ArgumentList @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/DIR=`"$root`"", "/LOG=`"$log`"", '/MODE=local', '/AUTOSTART=1', "/ADMINMODE=$Privilege") -PassThru
    if (-not $process.WaitForExit(180000)) { Stop-Process -Id $process.Id -Force; throw "$Label timed out" }
    $process.Refresh()
    Write-Host "$Label exit=$($process.ExitCode)"
    if ($process.ExitCode -ne 0) { Get-Content -LiteralPath $log -Tail 100 | Write-Host }
    return $process.ExitCode
}
if ((Invoke-TestSetup $LegacySetup 'official-install') -ne 0) { throw 'Official Setup failed' }
$binary = Join-Path $root 'bin\agentdock.exe'
$version = & $binary version --json | ConvertFrom-Json
if ($version.version -ne '0.8.3') { throw "Expected real official v0.8.3, got $($version.version)" }
$health = Invoke-WebRequest -UseBasicParsing 'http://127.0.0.1:8765/healthz'
if ($health.StatusCode -ne 200) { throw 'Official Core not healthy' }
$tokenPath = Join-Path $root 'auth-token.dpapi'
$tokenHash = (Get-FileHash $tokenPath).Hash
if ($BrokenSetup) {
    if ((Invoke-TestSetup $BrokenSetup 'broken-upgrade') -eq 0) { throw 'Broken upgrade unexpectedly passed' }
    & (Join-Path $PSScriptRoot '..\install\repair-windows-preview.ps1') -InstallRoot $root -AgentDockBinary $RecoveryBinary
}
$exitCode = Invoke-TestSetup $TargetSetup 'preview-upgrade'
if ($ExpectFailure) {
    if ($exitCode -eq 0) { throw 'Baseline unexpectedly passed; reproduction needs investigation' }
    Write-Host 'Reproduced released preview upgrade failure from official v0.8.3.'
    exit 0
}
if ($exitCode -ne 0) { throw 'Patched Setup upgrade failed' }
$version = & $binary version --json | ConvertFrom-Json
if ($version.version -ne '0.8.3-computer-use.2') { throw "Unexpected target version $($version.version)" }
if ((Get-FileHash $tokenPath).Hash -ne $tokenHash) { throw 'Upgrade changed existing credential' }
$health = Invoke-WebRequest -UseBasicParsing 'http://127.0.0.1:8765/healthz'
if ($health.StatusCode -ne 200) { throw 'Upgraded Core not healthy' }
$deadline = [DateTime]::UtcNow.AddSeconds(15)
do {
    $tray = @(Get-CimInstance Win32_Process -Filter "Name='agentdock-tray.exe'" | Where-Object { $_.ExecutablePath -like "$root\versions\*" })
    if ($tray.Count -gt 0) { break }
    Start-Sleep -Milliseconds 500
} while ([DateTime]::UtcNow -lt $deadline)
if ($tray.Count -eq 0) { throw 'Upgraded Tray did not remain running' }
Write-Host 'Official v0.8.3 to preview upgrade passed; existing credential preserved.'
