# Relais 一行安装（Windows）：irm <base>/install.ps1 | iex
$ErrorActionPreference = "Stop"
$Base = "__BASE_URL__"
$Dest = "$HOME\relais-bin"
New-Item -ItemType Directory -Force $Dest | Out-Null
Invoke-WebRequest "$Base/download/relais-windows-amd64.exe" -OutFile "$Dest\relais.exe"
Unblock-File "$Dest\relais.exe"
$p = [Environment]::GetEnvironmentVariable("Path","User")
if ($p -notlike "*$Dest*") { [Environment]::SetEnvironmentVariable("Path", "$p;$Dest", "User") }
& "$Dest\relais.exe" version
Write-Host "安装完成。关掉窗口重开，然后 relais login ... 再 relais setup"
