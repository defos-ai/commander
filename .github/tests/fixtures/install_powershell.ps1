param(
    [Parameter(Mandatory = $true)] [string] $Repository,
    [Parameter(Mandatory = $true)] [string] $Fixture,
    [string] $Architecture = 'AMD64',
    [string] $Version = 'latest',
    [switch] $Preview,
    [switch] $Replace
)

[Console]::Error.WriteLine('Installer fixture started (PowerShell ' + $PSVersionTable.PSVersion.ToString() + ')')
$ErrorActionPreference = 'Stop'
$env:OS = 'Windows_NT'
$env:PROCESSOR_ARCHITECTURE = $Architecture
$env:PROCESSOR_ARCHITEW6432 = ''
$env:LOCALAPPDATA = Join-Path $Fixture 'AppData'
$env:COMMANDER_INSTALLER_FIXTURE = $Fixture
. (Join-Path $Repository '.github/tests/fakes/install_web.ps1')
[Console]::Error.WriteLine('Installer fixture loaded local downloads')

$options = @{ Version = $Version }
if ($Preview) {
    $options.WhatIf = $true
}
if ($Replace) {
    $options.Force = $true
}
& (Join-Path $Repository 'install.ps1') @options
