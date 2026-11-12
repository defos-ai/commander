param(
    [Parameter(Mandatory = $true)] [string] $Repository,
    [Parameter(Mandatory = $true)] [string] $Fixture,
    [string] $Architecture = 'AMD64',
    [string] $Version = 'latest',
    [switch] $Preview,
    [switch] $Replace
)

$fixtureTimer = [Diagnostics.Stopwatch]::StartNew()
function Write-InstallerFixtureStage {
    param([string] $Stage)

    [Console]::Error.WriteLine('Installer fixture ' + $Stage + ' (' + $fixtureTimer.ElapsedMilliseconds + 'ms)')
}

Write-InstallerFixtureStage ('started (PowerShell ' + $PSVersionTable.PSVersion.ToString() + ')')
$ErrorActionPreference = 'Stop'
# Load this shell's built-ins before overriding architecture or triggering module discovery.
foreach ($module in @('Microsoft.PowerShell.Management', 'Microsoft.PowerShell.Utility')) {
    Write-InstallerFixtureStage ('loading built-in module ' + $module)
    $manifest = [IO.Path]::Combine($PSHOME, 'Modules', $module, "$module.psd1")
    Import-Module -Name $manifest -ErrorAction Stop
}
Write-InstallerFixtureStage 'loaded built-in modules'
$env:OS = 'Windows_NT'
$env:PROCESSOR_ARCHITECTURE = $Architecture
$env:PROCESSOR_ARCHITEW6432 = ''
$env:LOCALAPPDATA = Join-Path $Fixture 'AppData'
$env:COMMANDER_INSTALLER_FIXTURE = $Fixture
. (Join-Path $Repository '.github/tests/fakes/install_web.ps1')
Write-InstallerFixtureStage 'loaded local downloads'

$options = @{ Version = $Version }
if ($Preview) {
    $options.WhatIf = $true
}
if ($Replace) {
    $options.Force = $true
}
Write-InstallerFixtureStage 'running installer'
& (Join-Path $Repository 'install.ps1') @options
Write-InstallerFixtureStage 'finished installer'
