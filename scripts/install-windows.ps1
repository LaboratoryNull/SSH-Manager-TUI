$ErrorActionPreference = "Stop"
# Invoke-WebRequest in Windows PowerShell 5.1 is many times slower while it
# draws the progress bar.
$ProgressPreference = "SilentlyContinue"
# Add TLS 1.2 without dropping protocols that are already enabled.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Repository = if ($env:SSHM_GITHUB_REPOSITORY) { $env:SSHM_GITHUB_REPOSITORY } else { "LaboratoryNull/SSH-Manager-TUI" }
$Version = if ($env:SSHM_VERSION) { $env:SSHM_VERSION } else { "latest" }

# RuntimeInformation.OSArchitecture is missing or returns $null in Windows
# PowerShell 5.1 on older .NET Framework builds, and calling .ToString() on it
# failed with "You cannot call a method on a null-valued expression" (#2).
# Use it when it works and fall back to the OS environment variables.
# PROCESSOR_ARCHITEW6432 holds the real OS architecture inside a 32-bit
# PowerShell on a 64-bit Windows.
function Get-OSArchitecture {
    try {
        $Value = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
        if ($null -ne $Value) {
            return "$Value".ToLowerInvariant()
        }
    } catch {
    }
    foreach ($Value in @($env:PROCESSOR_ARCHITEW6432, $env:PROCESSOR_ARCHITECTURE)) {
        if ($Value) {
            return "$Value".ToLowerInvariant()
        }
    }
    return "unknown"
}

$Architecture = Get-OSArchitecture
switch ($Architecture) {
    { $_ -in @("x64", "amd64") } { $ReleaseArchitecture = "amd64" }
    "arm64" { $ReleaseArchitecture = "arm64" }
    default { throw "Unsupported Windows architecture: $Architecture. sshm is built for 64-bit Windows (x64 or ARM64)." }
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
    $LocalAppData = if ($env:LOCALAPPDATA) { $env:LOCALAPPDATA } else { [Environment]::GetFolderPath("LocalApplicationData") }
    Join-Path $LocalAppData "Programs\sshm"
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
