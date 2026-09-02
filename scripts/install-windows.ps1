$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repository = if ($env:SSHM_GITHUB_REPOSITORY) { $env:SSHM_GITHUB_REPOSITORY } else { "LaboratoryNull/SSH-Manager-TUI" }
$Version = if ($env:SSHM_VERSION) { $env:SSHM_VERSION } else { "latest" }

$Architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
switch ($Architecture) {
    "x64" { $ReleaseArchitecture = "amd64" }
    "arm64" { $ReleaseArchitecture = "arm64" }
    default { throw "Unsupported Windows architecture: $Architecture" }
}

$Asset = "sshm-windows-$ReleaseArchitecture.exe"
$DownloadBase = if ($Version -eq "latest") {
    "https://github.com/$Repository/releases/latest/download"
} else {
    "https://github.com/$Repository/releases/download/$Version"
}
$InstallDirectory = if ($env:SSHM_INSTALL_DIR) {
    $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($env:SSHM_INSTALL_DIR)
} else {
    Join-Path $env:LOCALAPPDATA "Programs\sshm"
}

$TemporaryDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("sshm-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $TemporaryDirectory | Out-Null

try {
    $TemporaryBinary = Join-Path $TemporaryDirectory $Asset
    $ChecksumsFile = Join-Path $TemporaryDirectory "checksums.txt"
    Invoke-WebRequest -UseBasicParsing -Uri "$DownloadBase/$Asset" -OutFile $TemporaryBinary
    Invoke-WebRequest -UseBasicParsing -Uri "$DownloadBase/checksums.txt" -OutFile $ChecksumsFile

    $EscapedAsset = [regex]::Escape($Asset)
    $ChecksumLine = Get-Content $ChecksumsFile | Where-Object { $_ -match "^([0-9a-fA-F]{64})\s+\*?$EscapedAsset$" } | Select-Object -First 1
    if (-not $ChecksumLine) {
        throw "Checksum for $Asset was not found in the release."
    }
    $ExpectedHash = ([regex]::Match($ChecksumLine, "^[0-9a-fA-F]{64}")).Value.ToLowerInvariant()
    $ActualHash = (Get-FileHash -Algorithm SHA256 $TemporaryBinary).Hash.ToLowerInvariant()
    if ($ActualHash -ne $ExpectedHash) {
        throw "SHA-256 verification failed for $Asset."
    }

    New-Item -ItemType Directory -Force -Path $InstallDirectory | Out-Null
    Copy-Item -Force $TemporaryBinary (Join-Path $InstallDirectory "sshm.exe")
    @'
@echo off
"%~dp0sshm.exe" %*
'@ | Set-Content -Encoding Ascii (Join-Path $InstallDirectory "ssh-manager.cmd")

    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $UserPathEntries = @($UserPath -split ";" | Where-Object { $_ })
    if (-not ($UserPathEntries | Where-Object { $_.TrimEnd("\") -ieq $InstallDirectory.TrimEnd("\") })) {
        $NewUserPath = (@($UserPathEntries) + $InstallDirectory) -join ";"
        [Environment]::SetEnvironmentVariable("Path", $NewUserPath, "User")
    }
    if (-not (($env:Path -split ";") | Where-Object { $_.TrimEnd("\") -ieq $InstallDirectory.TrimEnd("\") })) {
        $env:Path = "$env:Path;$InstallDirectory"
    }

    Write-Host "sshm $Version installed in $InstallDirectory"
    Write-Host "Run: sshm"
} finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $TemporaryDirectory
}
