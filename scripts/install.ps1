$ErrorActionPreference = "Stop"

$Repository = "chensunlai/codex-utils"
$Version = if ($env:CODEX_UTILS_VERSION) { $env:CODEX_UTILS_VERSION } else { "latest" }
$InstallDir = if ($env:CODEX_UTILS_INSTALL_DIR) {
    $env:CODEX_UTILS_INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA "codex-utils\bin"
}

$Architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
switch ($Architecture) {
    "x64" { $Arch = "amd64" }
    "arm64" { $Arch = "arm64" }
    default { throw "Unsupported architecture: $Architecture" }
}

$Asset = "codex-utils_windows_$Arch.zip"
if ($Version -eq "latest") {
    $DownloadBase = "https://github.com/$Repository/releases/latest/download"
} else {
    $Tag = if ($Version.StartsWith("v")) { $Version } else { "v$Version" }
    $DownloadBase = "https://github.com/$Repository/releases/download/$Tag"
}

$Temporary = Join-Path ([System.IO.Path]::GetTempPath()) ("codex-utils-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $Temporary | Out-Null
try {
    $Archive = Join-Path $Temporary $Asset
    $Checksums = Join-Path $Temporary "checksums.txt"
    Write-Host "Downloading $Asset..."
    Invoke-WebRequest -UseBasicParsing -Uri "$DownloadBase/$Asset" -OutFile $Archive
    Invoke-WebRequest -UseBasicParsing -Uri "$DownloadBase/checksums.txt" -OutFile $Checksums

    $ChecksumLine = Get-Content $Checksums | Where-Object {
        $Fields = $_.Trim() -split "\s+"
        $Fields.Count -ge 2 -and $Fields[1] -eq $Asset
    } | Select-Object -First 1
    if (-not $ChecksumLine) {
        throw "Checksum entry is missing for $Asset"
    }
    $Expected = ($ChecksumLine.Trim() -split "\s+")[0].ToLowerInvariant()
    $Actual = (Get-FileHash -Algorithm SHA256 $Archive).Hash.ToLowerInvariant()
    if ($Expected -ne $Actual) {
        throw "Checksum verification failed for $Asset"
    }

    $Expanded = Join-Path $Temporary "expanded"
    Expand-Archive -Path $Archive -DestinationPath $Expanded -Force
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $Destination = Join-Path $InstallDir "codex-utils.exe"
    Copy-Item (Join-Path $Expanded "codex-utils.exe") $Destination -Force

    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $PathEntries = if ($UserPath) { $UserPath -split ";" } else { @() }
    if (-not ($PathEntries | Where-Object { $_.TrimEnd("\") -ieq $InstallDir.TrimEnd("\") })) {
        $NewPath = if ($UserPath) { "$UserPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    }
    if (-not (($env:Path -split ";") | Where-Object { $_.TrimEnd("\") -ieq $InstallDir.TrimEnd("\") })) {
        $env:Path = "$InstallDir;$env:Path"
    }
    Write-Host "Installed codex-utils to $Destination"
} finally {
    Remove-Item $Temporary -Recurse -Force -ErrorAction SilentlyContinue
}
