[CmdletBinding()]
param(
    [string] $InstallRoot = (Join-Path $env:LOCALAPPDATA 'AgentDock'),
    [string] $AgentDockBinary = (Join-Path $PSScriptRoot 'agentdock.exe')
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$InstallRoot = (Resolve-Path -LiteralPath $InstallRoot).Path
$AgentDockBinary = (Resolve-Path -LiteralPath $AgentDockBinary).Path
$installedBinary = Join-Path $InstallRoot 'bin\agentdock.exe'
$broker = Join-Path $PSScriptRoot 'launch-windows-process.ps1'

# Narrow recovery for the released preview's standard-user legacy rollback bug.
# Do not acknowledge unrelated failures, pending trials, or missing elevated tasks.
$inspection = (& $AgentDockBinary install inspect --state-root $InstallRoot | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect install state.' }
if ($inspection.state -ne 'failed') { throw 'No failed preview transaction to recover.' }
$transaction = Get-Content (Join-Path $InstallRoot 'install\transaction.json') -Raw | ConvertFrom-Json
if ($transaction.failure.code -ne 'external_rollback_failed' -or
    $transaction.transaction_id -ne $inspection.transaction_id -or
    $transaction.source_version -ne 'v0.8.3' -or
    $transaction.target_version -notin @('v0.8.3-computer-use.1', 'v0.8.3-computer-use.2')) {
    throw 'This is not the known v0.8.3 preview rollback failure. State was not cleared.'
}
if ($inspection.pointer_state -ne 'missing' -and
    ($inspection.pointer_state -ne 'committed' -or $inspection.pointer_active_version -ne 'v0.8.3')) {
    throw 'The original v0.8.3 generation has not been restored. State was not cleared.'
}
$manifest = Get-Content (Join-Path $InstallRoot 'runtime.json') -Raw | ConvertFrom-Json
if ($manifest.privilege_mode -ne 'standard' -or
    -not [string]::Equals([IO.Path]::GetFullPath($manifest.agentdock_binary), $installedBinary, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Recovery requires the restored standard-user runtime at this install root.'
}
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$ownerPath = Join-Path $InstallRoot 'credential-owner-sid.txt'
if ((Test-Path $ownerPath) -and (Get-Content $ownerPath -Raw).Trim() -ne $identity.User.Value) {
    throw 'Run recovery as the original signed-in Windows user.'
}
# Official v0.8.3 predates the owner-SID marker. DPAPI still proves that this
# account can open the existing credential; never print the decrypted value.
Add-Type -AssemblyName System.Security
$protected = [Convert]::FromBase64String((Get-Content (Join-Path $InstallRoot 'auth-token.dpapi') -Raw).Trim())
$plain = [Security.Cryptography.ProtectedData]::Unprotect($protected, [Text.Encoding]::UTF8.GetBytes('agentdock.startup.v1'), [Security.Cryptography.DataProtectionScope]::CurrentUser)
if ($plain.Length -eq 0) { throw 'Existing credential is empty.' }
[Array]::Clear($plain, 0, $plain.Length)
$tasks = @(Get-ScheduledTask -ErrorAction Stop | Where-Object { $_.TaskPath -eq '\' -and $_.TaskName -eq 'AgentDock' })
if ($tasks.Count -gt 0) { throw 'An AgentDock scheduled task remains; automatic standard-user recovery is not applicable.' }
$run = Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
foreach ($name in @('AgentDock', 'AgentDockTray', 'AgentDockCloudflared')) {
    $property = $run.PSObject.Properties[$name]
    if ($null -ne $property -and -not ([string] $property.Value).Contains($InstallRoot + '\')) {
        throw "Startup entry $name belongs to another path. State was not cleared."
    }
}
$version = (& $installedBinary version --json | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or $version.version -ne '0.8.3') { throw 'Installed Core is not restored v0.8.3.' }
$backup = Join-Path $InstallRoot ('logs\installer\preview-recovery-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $backup -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $InstallRoot 'install') -Destination $backup -Recurse
Copy-Item -LiteralPath (Join-Path $InstallRoot 'runtime.json') -Destination $backup
if (Test-Path (Join-Path $InstallRoot 'active-version.json')) {
    Copy-Item -LiteralPath (Join-Path $InstallRoot 'active-version.json') -Destination $backup
}

& $broker -FilePath $installedBinary -AgentDockBinary $AgentDockBinary -Arguments "service start --runtime-root `"$InstallRoot`"" -WaitForExit -TimeoutSeconds 90
$status = (& $installedBinary service status --runtime-root $InstallRoot | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or -not $status.running -or -not $status.healthy) { throw 'Original Core is not healthy. State was not cleared.' }
if ($manifest.tunnel_mode -in @('quick', 'named')) {
    & $broker -FilePath $installedBinary -AgentDockBinary $AgentDockBinary -Arguments "tunnel start --runtime-root `"$InstallRoot`"" -WaitForExit -TimeoutSeconds 120
}
& $broker -FilePath (Join-Path $InstallRoot 'bin\agentdock-tray.exe') -AgentDockBinary $AgentDockBinary -Arguments '--background'
& $AgentDockBinary install abandon --install-root $InstallRoot --runtime-root $InstallRoot --transaction-id $inspection.transaction_id | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Recovery confirmation failed.' }
$after = (& $AgentDockBinary install inspect --state-root $InstallRoot | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or $after.state -ne 'rolled_back') { throw 'Recovered state was not confirmed.' }
Write-Host "Original v0.8.3 recovered. Configuration retained. Backup: $backup"
Write-Host 'You can now run the fixed Setup.'
