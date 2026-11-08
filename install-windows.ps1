#Requires -Version 5.1
<#
  Compila e instala Cofre en Windows 10/11.
  - cofre.exe           -> comandos (cofre enviar, cofre registrar...)
  - cofre-servidor.exe  -> servidor en segundo plano, sin ventana
  Arranca solo al iniciar sesión (Programador de tareas). No necesita administrador,
  salvo para abrir el puerto en el firewall (Windows lo pedirá).

  Uso:  powershell -ExecutionPolicy Bypass -File .\install-windows.ps1
        -SinConfirmar  no pregunta nada (no confía el certificado en este PC)
#>
param([switch]$SinConfirmar)

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

function Step($m) { Write-Host "-> $m" -ForegroundColor Cyan }

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host 'No encuentro Go (>= 1.26). Instálalo con:  winget install GoLang.Go' -ForegroundColor Red
    exit 1
}

$Dir  = Join-Path $env:LOCALAPPDATA 'Programs\Cofre'
$Data = Join-Path $env:LOCALAPPDATA 'Cofre'
$Log  = Join-Path $Data 'cofre.log'
$Exe  = Join-Path $Dir 'cofre.exe'
$Srv  = Join-Path $Dir 'cofre-servidor.exe'
New-Item -ItemType Directory -Force -Path $Dir, $Data | Out-Null

Step 'Parando la versión anterior (si existe)'
Stop-ScheduledTask -TaskName 'Cofre' -ErrorAction SilentlyContinue
Get-Process 'cofre-servidor' -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Milliseconds 500

Step 'Compilando'
go build -trimpath -ldflags '-s -w' -o $Exe .
if ($LASTEXITCODE) { exit 1 }
go build -trimpath -ldflags '-s -w -H=windowsgui' -o $Srv .
if ($LASTEXITCODE) { exit 1 }

Step 'Añadiendo Cofre al PATH de tu usuario'
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ((($userPath -split ';') | Where-Object { $_ -eq $Dir }).Count -eq 0) {
    [Environment]::SetEnvironmentVariable('Path', (($userPath.TrimEnd(';') + ';' + $Dir).TrimStart(';')), 'User')
}
$env:Path += ";$Dir"

Step 'Creando la tarea que lo arranca al iniciar sesión'
$user      = "$env:USERDOMAIN\$env:USERNAME"
$action    = New-ScheduledTaskAction -Execute $Srv -Argument "serve -log `"$Log`""
$trigger   = New-ScheduledTaskTrigger -AtLogOn -User $user
$settings  = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
               -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -Priority 7
$principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName 'Cofre' -Description 'Cofre: comparte archivos en tu red local' `
    -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
Start-ScheduledTask -TaskName 'Cofre'
Start-Sleep -Seconds 3

if (-not (Get-Process 'cofre-servidor' -ErrorAction SilentlyContinue)) {
    Write-Host "El servidor no ha arrancado. Revisa el log:  Get-Content `"$Log`" -Tail 30" -ForegroundColor Red
    exit 1
}

Step 'Abriendo el puerto 8443 en el firewall (solo redes privadas)'
if (-not (Get-NetFirewallRule -DisplayName 'Cofre (red privada)' -ErrorAction SilentlyContinue)) {
    $rule = "New-NetFirewallRule -DisplayName 'Cofre (red privada)' -Direction Inbound -Program '$Srv' " +
            "-Protocol TCP -LocalPort 8443 -Action Allow -Profile Private"
    try {
        Start-Process powershell -Verb RunAs -Wait -WindowStyle Hidden -ArgumentList '-NoProfile', '-Command', $rule
    } catch {
        Write-Host '   No se aceptó el permiso de administrador: crea la regla a mano (ver README).' -ForegroundColor Yellow
    }
}

$ca = Join-Path $Data 'tls\cofre-ca.crt'
if ((Test-Path $ca) -and -not $SinConfirmar) {
    $ans = Read-Host '¿Confiar en el certificado de Cofre en este PC (necesario para registrar tu llave aquí)? [S/n]'
    if ($ans -notmatch '^[nN]') {
        # Windows mostrará una advertencia de seguridad: pulsa "Sí".
        Import-Certificate -FilePath $ca -CertStoreLocation Cert:\CurrentUser\Root | Out-Null
    }
}

$hostName = $env:COMPUTERNAME.ToLower() + '.local'
Write-Host ''
Write-Host "OK  Cofre instalado y en marcha  ->  https://${hostName}:8443" -ForegroundColor Green
Write-Host ''
Write-Host '  Siguientes pasos (abre una terminal NUEVA para que se cargue el PATH):'
Write-Host '    cofre ca                  certificado para tus dispositivos'
Write-Host '    cofre registrar           registrar tu llave (desde este PC)'
Write-Host '    cofre enviar archivo.pdf  enviar algo'
Write-Host "    Get-Content `"$Log`" -Wait   ver el log"
Write-Host ''
Write-Host '  Importante: tu wifi debe estar marcada como red PRIVADA' -ForegroundColor Yellow
Write-Host '  (Configuración > Red e Internet > Wi-Fi > tu red > Tipo de perfil de red > Privada).'
Write-Host ''
