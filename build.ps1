# Сборка RANDSEC -> dist\randsec.exe
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Force dist | Out-Null
go vet ./...
go build -trimpath -ldflags "-s -w" -o dist\randsec.exe ./cmd/randsec
Get-Item dist\randsec.exe | Select-Object FullName, Length
