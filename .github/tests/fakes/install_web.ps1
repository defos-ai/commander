function Send-InstallerFixtureRedirect {
    param([string] $Location)

    if ($PSVersionTable.PSVersion.Major -ge 6) {
        $response = [Net.Http.HttpResponseMessage]::new([Net.HttpStatusCode]::Redirect)
        $response.Headers.Location = [Uri] $Location
        throw [Microsoft.PowerShell.Commands.HttpResponseException]::new('Fixture redirect limit', $response)
    }
    return [PSCustomObject]@{ StatusCode = 302; Headers = @{ Location = $Location } }
}

function Invoke-WebRequest {
    [CmdletBinding()]
    param(
        [string] $Uri,
        [string] $Method,
        [switch] $UseBasicParsing,
        [int] $MaximumRedirection,
        [int] $TimeoutSec
    )

    if (-not $UseBasicParsing -or $MaximumRedirection -ne 0 -or $TimeoutSec -le 0) {
        throw 'The installer did not constrain its web request.'
    }
    $fixtureRoot = $env:COMMANDER_INSTALLER_FIXTURE
    [IO.File]::AppendAllText((Join-Path $fixtureRoot 'requests.log'), "$Method $Uri`n")
    if ($Uri -eq 'https://github.com/defos-ai/commander/releases/latest' -and $Method -eq 'Head') {
        return Send-InstallerFixtureRedirect -Location ([IO.File]::ReadAllText((Join-Path $fixtureRoot 'latest-url')))
    }
    if ($Method -ne 'Get') {
        throw 'Unexpected installer request method.'
    }
    $parsed = [Uri] $Uri
    $asset = [IO.Path]::GetFileName($parsed.AbsolutePath)
    if ($Uri -eq "https://github.com/defos-ai/commander/releases/download/v1.2.3/$asset") {
        return Send-InstallerFixtureRedirect -Location "https://release-assets.githubusercontent.com/fixture/$asset"
    }
    if ($Uri -ne "https://release-assets.githubusercontent.com/fixture/$asset") {
        throw 'Installer did not use the pinned release.'
    }
    if ($asset -eq 'checksums.txt') {
        $bytes = [IO.File]::ReadAllBytes((Join-Path $fixtureRoot 'checksums.txt'))
    }
    elseif ($asset -eq 'commander_windows_amd64.exe' -or $asset -eq 'commander_windows_arm64.exe') {
        $bytes = [IO.File]::ReadAllBytes((Join-Path $fixtureRoot 'download'))
    }
    else {
        throw 'Installer selected an unexpected asset.'
    }
    return [PSCustomObject]@{
        StatusCode = 200
        Headers = @{}
        RawContentStream = [IO.MemoryStream]::new([byte[]] $bytes)
    }
}
