#Requires -Version 5.1
<#
  Desinstala Cofre de Windows.  Con -Purge borra también los datos cifrados.
  Uso:  powershell -ExecutionPolicy Bypass -File .\uninstall-windows.ps1 [-Purge]
#>
param([switch]$Purge)
$ErrorActionPreference = 'SilentlyContinue'

$Dir  = Join-Path $env:LOCALAPPDATA 'Programs\Cofre'
$Data = Join-Path $env:LOCALAPPDATA 'Cofre'

Stop-ScheduledTask -TaskName 'Cofre'
Unregister-ScheduledTask -TaskName 'Cofre' -Confirm:$false
Get-Process 'cofre-servidor' | Stop-Process -Force
Start-Sleep -Milliseconds 500
Remove-Item -Recurse -Force $Dir

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
[Environment]::SetEnvironmentVariable('Path', (($userPath -split ';' | Where-Object { $_ -and $_ -ne $Dir }) -join ';'), 'User')

# Quitar el certificado de confianza (Windows pedirá confirmación)
Get-ChildItem Cert:\CurrentUser\Root | Where-Object { $_.Subject -like '*Cofre CA*' } | Remove-Item

if (Get-NetFirewallRule -DisplayName 'Cofre (red privada)') {
    Start-Process powershell -Verb RunAs -Wait -WindowStyle Hidden -ArgumentList '-NoProfile', '-Command',
        "Remove-NetFirewallRule -DisplayName 'Cofre (red privada)'"
}

if ($Purge) {
    Remove-Item -Recurse -Force $Data
    Write-Host 'OK  Cofre desinstalado y datos borrados.' -ForegroundColor Green
} else {
    Write-Host "OK  Cofre desinstalado. Tus datos siguen en $Data (bórralos con -Purge)." -ForegroundColor Green
}
