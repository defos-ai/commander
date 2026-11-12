#Requires -Version 5.1
[CmdletBinding(SupportsShouldProcess = $true, PositionalBinding = $false)]
param(
    [string] $Version = 'latest',
    [string] $BinDir,
    [switch] $Force
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-InstallerResponse {
    param([Uri] $Uri, [string] $Method = 'Get')

    if ($Uri.Scheme -ne 'https') {
        throw 'Installer downloads require HTTPS.'
    }
    # PowerShell 5.1 returns redirects; PowerShell 7 throws with the response attached.
    try {
        $response = Invoke-WebRequest -Uri $Uri.AbsoluteUri -Method $Method -UseBasicParsing -MaximumRedirection 0 -TimeoutSec 120 -ErrorAction SilentlyContinue
    }
    catch {
        $property = $_.Exception.PSObject.Properties['Response']
        if ($null -eq $property) {
            throw 'Could not download the Commander release. Check network access and the release version.'
        }
        $response = $property.Value
    }
    if ($null -eq $response) {
        throw 'Could not download the Commander release. Check network access and the release version.'
    }
    return $response
}

function Get-InstallerRedirect {
    param($Response, [Uri] $Uri)

    if ($Response -is [System.Net.Http.HttpResponseMessage]) {
        $location = $Response.Headers.Location
    }
    else {
        $location = @($Response.Headers['Location'])[0]
    }
    if ($null -eq $location -or [string]::IsNullOrWhiteSpace([string] $location)) {
        throw 'The release server returned a redirect without a location.'
    }
    $next = [Uri]::new($Uri, [string] $location)
    if ($next.Scheme -ne 'https' -or -not [string]::IsNullOrEmpty($next.UserInfo)) {
        throw 'The release server returned an unsafe redirect.'
    }
    return $next
}

function Save-InstallerDownload {
    param([Uri] $Uri, [string] $Path)

    for ($redirects = 0; $redirects -lt 10; $redirects++) {
        $response = Get-InstallerResponse -Uri $Uri
        $status = [int] $response.StatusCode
        if ($status -in @(301, 302, 303, 307, 308)) {
            $Uri = Get-InstallerRedirect -Response $response -Uri $Uri
            continue
        }
        if ($status -ne 200) {
            throw "The release download failed with HTTP $status."
        }
        $stream = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        try {
            $response.RawContentStream.Position = 0
            $response.RawContentStream.CopyTo($stream)
            $stream.Flush()
        }
        finally {
            $stream.Dispose()
            $response.RawContentStream.Dispose()
        }
        return
    }
    throw 'The release download exceeded its redirect limit.'
}

function Get-InstallerTarget {
    param([string] $Path)

    try {
        $attributes = [IO.File]::GetAttributes($Path)
    }
    catch [IO.FileNotFoundException] {
        return $false
    }
    catch [IO.DirectoryNotFoundException] {
        return $false
    }
    if (($attributes -band [IO.FileAttributes]::Directory) -ne 0 -or
        ($attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw 'The install destination must be a regular file, not a directory or reparse point.'
    }
    return $true
}

if ($env:OS -ne 'Windows_NT') {
    throw 'This installer supports Windows. Use install.sh on Linux or macOS.'
}
if ($Version -cne 'latest' -and $Version -cnotmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') {
    throw 'Version must be latest or a stable version such as v0.5.0.'
}
$osArchitecture = $env:PROCESSOR_ARCHITEW6432
if ([string]::IsNullOrEmpty($osArchitecture)) {
    $osArchitecture = $env:PROCESSOR_ARCHITECTURE
}
if ([string]::IsNullOrEmpty($osArchitecture)) {
    throw 'Could not determine the Windows operating-system architecture.'
}
switch ($osArchitecture.ToUpperInvariant()) {
    'AMD64' { $architecture = 'amd64' }
    'ARM64' { $architecture = 'arm64' }
    default { throw 'Commander supports Windows amd64 and arm64.' }
}
if ([string]::IsNullOrEmpty($BinDir)) {
    if ([string]::IsNullOrEmpty($env:LOCALAPPDATA)) {
        throw 'LOCALAPPDATA is unavailable; choose an install directory with -BinDir.'
    }
    $BinDir = Join-Path $env:LOCALAPPDATA 'Commander\bin'
}
$installDirectory = [IO.Path]::GetFullPath($BinDir)
$destination = Join-Path $installDirectory 'commander.exe'
$existing = Get-InstallerTarget -Path $destination

[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
if ($PSVersionTable.PSVersion.Major -lt 6) {
    Add-Type -AssemblyName System.Net.Http
}
$releaseRoot = 'https://github.com/defos-ai/commander/releases'
$selectedVersion = $Version
if ($Version -eq 'latest') {
    $latestUri = [Uri] "$releaseRoot/latest"
    $response = Get-InstallerResponse -Uri $latestUri -Method 'Head'
    if ([int] $response.StatusCode -notin @(301, 302, 303, 307, 308)) {
        throw 'Could not resolve the latest Commander release.'
    }
    $releaseUri = Get-InstallerRedirect -Response $response -Uri $latestUri
    if ($releaseUri.AbsoluteUri -cnotmatch '^https://github\.com/defos-ai/commander/releases/tag/(v[0-9]+\.[0-9]+\.[0-9]+)$') {
        throw 'The latest Commander release did not resolve to a stable version.'
    }
    $selectedVersion = $Matches[1]
}
$asset = "commander_windows_$architecture.exe"
$assetRoot = "$releaseRoot/download/$selectedVersion"
Write-Output "Version: $selectedVersion"
Write-Output "Platform: windows/$architecture"
Write-Output "Download: $assetRoot/$asset"
Write-Output "Install to: $destination"
if ($existing) {
    Write-Output 'Existing file: identical bytes are kept; replacement requires -Force.'
}
if (-not $PSCmdlet.ShouldProcess($destination, "Install checksum-verified Commander $selectedVersion")) {
    return
}

$downloadRoot = Join-Path ([IO.Path]::GetTempPath()) ('commander-install-' + [Guid]::NewGuid().ToString('N'))
$stagePath = $null
try {
    [void] [IO.Directory]::CreateDirectory($downloadRoot)
    $checksumPath = Join-Path $downloadRoot 'checksums.txt'
    $binaryPath = Join-Path $downloadRoot $asset
    Save-InstallerDownload -Uri "$assetRoot/checksums.txt" -Path $checksumPath
    $expected = @()
    foreach ($line in [IO.File]::ReadAllLines($checksumPath)) {
        $fields = @($line.Trim() -split '\s+')
        $selected = @($fields | Where-Object { $_ -eq $asset -or $_ -eq "*$asset" })
        if ($selected.Count -eq 0) {
            continue
        }
        if ($fields.Count -ne 2 -or $selected.Count -ne 1 -or
            ($fields[1] -ne $asset -and $fields[1] -ne "*$asset") -or $fields[0] -notmatch '^[0-9a-fA-F]{64}$') {
            throw 'The release contains a malformed checksum for the selected binary.'
        }
        $expected += $fields[0].ToUpperInvariant()
    }
    if ($expected.Count -ne 1) {
        throw 'The release must contain exactly one valid checksum for the selected binary.'
    }
    $existing = Get-InstallerTarget -Path $destination
    if ($existing -and (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash -eq $expected[0]) {
        Write-Output "Commander $selectedVersion is already installed at $destination."
        return
    }
    if ($existing -and -not $Force) {
        throw 'A different file is already installed; review it and rerun with -Force to replace it.'
    }
    Save-InstallerDownload -Uri "$assetRoot/$asset" -Path $binaryPath
    if ((Get-FileHash -LiteralPath $binaryPath -Algorithm SHA256).Hash -ne $expected[0]) {
        throw 'Commander checksum verification failed; nothing was installed.'
    }
    [void] [IO.Directory]::CreateDirectory($installDirectory)
    $stagePath = Join-Path $installDirectory ('.commander-' + [Guid]::NewGuid().ToString('N') + '.tmp')
    [IO.File]::Copy($binaryPath, $stagePath, $false)
    $existing = Get-InstallerTarget -Path $destination
    if ($existing) {
        if (-not $Force) {
            throw 'The install destination changed; review it before replacing it with -Force.'
        }
        # A plain $null is converted to an empty backup path by PowerShell.
        [IO.File]::Replace($stagePath, $destination, [NullString]::Value)
    }
    else {
        [IO.File]::Move($stagePath, $destination)
    }
    $stagePath = $null
    Write-Output "Installed Commander $selectedVersion at $destination."
    Write-Output "Add $installDirectory to PATH if needed."
}
finally {
    if ($null -ne $stagePath -and [IO.File]::Exists($stagePath)) {
        [IO.File]::Delete($stagePath)
    }
    if ([IO.Directory]::Exists($downloadRoot)) {
        [IO.Directory]::Delete($downloadRoot, $true)
    }
}
