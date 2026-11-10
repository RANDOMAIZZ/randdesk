# Сборка RandDesk -> dist\randdesk.exe
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Force dist | Out-Null
go vet ./...
go build -trimpath -ldflags "-s -w" -o dist\randdesk.exe ./cmd/randdesk
Get-Item dist\randdesk.exe | Select-Object FullName, Length
