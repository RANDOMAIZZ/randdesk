@set "RD_SELF=%~f0" & powershell -NoProfile -ExecutionPolicy Bypass -Command "iex ((Get-Content -LiteralPath $env:RD_SELF -Raw -Encoding UTF8) -split '#RANDDESK-PS#', 3)[2]" & exit /b
#RANDDESK-PS#
# Полное удаление RANDSEC с этого ПК (Windows).
# Удаляет службу, программу, настройки и данные, правила брандмауэра и, по желанию, ZeroTier One.
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$ErrorActionPreference = 'Continue'
$self = $env:RD_SELF

$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) {
    Write-Host 'Нужны права администратора — Windows спросит разрешение, нажмите «Да».'
    try { Start-Process -FilePath $self -Verb RunAs } catch { Write-Host 'Удаление отменено.'; Start-Sleep 3 }
    return
}

Write-Host ''
Write-Host '  Удаление RANDSEC с этого ПК'
Write-Host '  Будут удалены служба RANDSEC, программа, все настройки (логин и пароль, список ПК,'
Write-Host '  разрешения, журнал) и правила брандмауэра RANDSEC.'
Write-Host ''
$ans = Read-Host '  Продолжить? [Д/н]'
if ($ans -match '^(н|n|нет|no)$') { Write-Host '  Отменено.'; Start-Sleep 2; return }
$zt = Read-Host '  Удалить также ZeroTier One (RANDSEC ставил его для связи через интернет)? [Д/н]'
$removeZT = -not ($zt -match '^(н|n|нет|no)$')
Set-Location $env:TEMP

# 1. Службы и процессы
foreach ($svc in 'RandDesk', 'RandDeskAgent') {
    if (Get-Service $svc -ErrorAction SilentlyContinue) {
        Stop-Service $svc -Force -ErrorAction SilentlyContinue
        sc.exe delete $svc | Out-Null
        Write-Host "  служба $svc удалена"
    }
}
Start-Sleep -Seconds 2
Get-Process randdesk*, randsec* -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue

# 2. ZeroTier One
$dirs = @('C:\Program Files\RandDesk', "$env:ProgramData\RandDesk", "$env:APPDATA\RandDesk")
if ($removeZT) {
    $apps = Get-ItemProperty HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*, HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\* -ErrorAction SilentlyContinue |
        Where-Object { $_.DisplayName -like 'ZeroTier*' -and $_.PSChildName -like '{*}' }
    foreach ($a in $apps) {
        Write-Host '  удаление ZeroTier One…'
        Start-Process msiexec.exe -ArgumentList '/x', $a.PSChildName, '/qn', '/norestart' -Wait | Out-Null
    }
    Get-Process zerotier* -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Get-Service ZeroTierOneService -ErrorAction SilentlyContinue | ForEach-Object { Stop-Service $_.Name -Force; sc.exe delete $_.Name | Out-Null }
    $dirs += "$env:ProgramData\ZeroTier", 'C:\Program Files (x86)\ZeroTier', 'C:\Program Files\ZeroTier'
}

# 3. Правила брандмауэра
$rules = Get-NetFirewallRule -ErrorAction SilentlyContinue | Where-Object {
    $_.DisplayName -like 'RandDesk*' -or $_.DisplayName -like 'RANDSEC*' -or $_.DisplayName -like 'randdesk*.exe' -or $_.DisplayName -like 'randsec*.exe' -or
    ($removeZT -and ($_.DisplayName -like 'ZeroTier*' -or $_.DisplayName -like 'zerotier*')) }
$n = @($rules).Count
$rules | Remove-NetFirewallRule -ErrorAction SilentlyContinue
Write-Host "  правил брандмауэра удалено: $n"

# 4. Запись в «Приложениях»
Remove-Item 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\RandDesk' -Recurse -Force -ErrorAction SilentlyContinue

# 5. Папки (в том числе папка с этим файлом)
foreach ($d in $dirs) {
    if (Test-Path -LiteralPath $d) {
        Remove-Item -LiteralPath $d -Recurse -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $d) {
            takeown /f $d /r /d y *> $null
            icacls $d /grant '*S-1-5-32-544:F' /t *> $null
            Remove-Item -LiteralPath $d -Recurse -Force -ErrorAction SilentlyContinue
        }
        if (Test-Path -LiteralPath $d) { Write-Host "  НЕ удалось удалить $d" } else { Write-Host "  удалено $d" }
    }
}

Write-Host ''
Write-Host '  RANDSEC удалён с этого ПК.'
Read-Host '  Нажмите Enter, чтобы закрыть окно'
