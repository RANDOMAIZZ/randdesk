# Сборка приложения RANDSEC для Android -> dist\randsec-<версия>-android.apk
# Нужны JDK 17+ и Android SDK (platforms;android-35, build-tools;35.0.0). Gradle не нужен.
#   .\android\build.ps1 [-Sdk путь] [-Jdk путь]
param(
    [string]$Sdk = $(if ($env:ANDROID_HOME) { $env:ANDROID_HOME } else { "$env:USERPROFILE\AndroidTools\sdk" }),
    [string]$Jdk = $(if ($env:JAVA_HOME) { $env:JAVA_HOME } else { (Get-ChildItem "$env:USERPROFILE\AndroidTools\jdk" -Directory | Select-Object -First 1).FullName }),
    [string]$Platform = 'android-35',
    [string]$BuildTools = '35.0.0',
    [switch]$Debuggable # отладочная сборка: adb run-as читает журналы приложения
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot
Set-Location $root

$bt = Join-Path $Sdk "build-tools\$BuildTools"
$androidJar = Join-Path $Sdk "platforms\$Platform\android.jar"
foreach ($p in $bt, $androidJar, "$Jdk\bin\javac.exe") { if (-not (Test-Path $p)) { throw "не найдено: $p" } }
$env:JAVA_HOME = $Jdk
$env:PATH = "$Jdk\bin;$env:PATH"

$version = (Select-String -Path internal\proto\proto.go -Pattern 'const Version = "(.+)"').Matches[0].Groups[1].Value
$parts = $version.Split('.') | ForEach-Object { [int]$_ }
$versionCode = $parts[0] * 10000 + $parts[1] * 100 + $parts[2]
$out = Join-Path $root 'build\android'
if (Test-Path $out) { Remove-Item -Recurse -Force $out }
New-Item -ItemType Directory -Force "$out\gen", "$out\classes", "$out\dex", "$out\lib", dist | Out-Null

function Run($exe) {
    & $exe @args
    if ($LASTEXITCODE -ne 0) { throw "$exe завершился с кодом $LASTEXITCODE" }
}

# 1. Go-часть: узел RANDSEC как «библиотека» lib*.so — Android разрешает запускать только файлы
#    из папки нативных библиотек приложения.
# x86_64 (эмуляторы, Chromebook): Go без cgo собирает для android только arm64, поэтому здесь
# статический файл для Linux — Android запускает его так же.
$abis = @{ 'arm64-v8a' = @{ GOOS = 'android'; GOARCH = 'arm64' }; 'x86_64' = @{ GOOS = 'linux'; GOARCH = 'amd64' } }
foreach ($abi in $abis.Keys) {
    New-Item -ItemType Directory -Force "$out\lib\$abi" | Out-Null
    $env:GOOS = $abis[$abi].GOOS; $env:GOARCH = $abis[$abi].GOARCH; $env:CGO_ENABLED = '0'
    Run go build -trimpath -ldflags "-s -w" -o "$out\lib\$abi\librandsec.so" ./cmd/randsec
}
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED

# 2. Ресурсы и манифест
[IO.File]::WriteAllText("$out\gen\BuildInfo.java", "package io.github.randomaizz.randsec;`nfinal class BuildInfo { static final String VERSION = `"$version`"; }`n") # без BOM
Run "$bt\aapt2.exe" compile --dir android\res -o "$out\res.zip"
Run "$bt\aapt2.exe" link -I $androidJar --manifest android\AndroidManifest.xml -o "$out\base.apk" `
    --java "$out\gen" --version-name $version --version-code $versionCode --min-sdk-version 26 --target-sdk-version 29 `
    -R "$out\res.zip" --auto-add-overlay @(if ($Debuggable) { '--debug-mode' })

# 3. Java -> dex
$srcs = @(Get-ChildItem -Recurse android\src, "$out\gen" -Filter *.java | ForEach-Object FullName)
Run javac -encoding UTF-8 -source 11 -target 11 -nowarn -classpath $androidJar -d "$out\classes" @srcs
$classes = @(Get-ChildItem -Recurse "$out\classes" -Filter *.class | ForEach-Object FullName)
Run "$bt\d8.bat" --release --min-api 26 --lib $androidJar --output "$out\dex" @classes

# 4. Собираем APK: ресурсы + classes.dex + lib/<abi>/librandsec.so
Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
$apk = "$out\unsigned.apk"
Copy-Item "$out\base.apk" $apk
$zip = [System.IO.Compression.ZipFile]::Open($apk, 'Update')
try {
    [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, "$out\dex\classes.dex", 'classes.dex')
    foreach ($f in Get-ChildItem -Recurse "$out\lib" -File) {
        $rel = 'lib/' + $f.FullName.Substring("$out\lib\".Length).Replace('\', '/')
        [void][System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $f.FullName, $rel)
    }
} finally { $zip.Dispose() }
Run "$bt\zipalign.exe" -f -p 4 $apk "$out\aligned.apk"

# 5. Подпись. Ключ хранится вне репозитория; без того же ключа Android не даст поставить обновление поверх.
$keyDir = Join-Path $env:USERPROFILE '.randsec'
$ks = Join-Path $keyDir 'android.keystore'
$pwFile = Join-Path $keyDir 'android.keystore.pass'
if (-not (Test-Path $ks)) {
    New-Item -ItemType Directory -Force $keyDir | Out-Null
    $pw = -join ((48..57) + (65..90) + (97..122) | Get-Random -Count 24 | ForEach-Object { [char]$_ })
    Set-Content -NoNewline -Encoding ascii $pwFile $pw
    Run keytool -genkeypair -keystore $ks -storepass $pw -keypass $pw -alias randsec -keyalg RSA -keysize 3072 `
        -validity 10000 -dname "CN=RANDSEC"
    Write-Host "Создан ключ подписи $ks — сохраните его: без него обновления не поставятся поверх."
}
$pw = Get-Content -Raw $pwFile
$final = Join-Path $root "dist\randsec-$version-android.apk"
Run "$bt\apksigner.bat" sign --ks $ks --ks-pass "pass:$pw" --ks-key-alias randsec --out $final "$out\aligned.apk"
Run "$bt\apksigner.bat" verify $final
Get-Item $final | Select-Object FullName, Length
