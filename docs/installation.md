# Install the native executable

The installer scripts are prepared for the next release. From a repository
checkout, preview the download and destination before installing a released binary.
They install release assets, not the checkout's source changes.

Linux or macOS:

```sh
sh ./install.sh --dry-run
sh ./install.sh
export PATH="$HOME/.local/bin:$PATH"
```

Windows PowerShell:

```powershell
.\install.ps1 -WhatIf
.\install.ps1
$env:Path = "$env:LOCALAPPDATA\Commander\bin;$env:Path"
```

The scripts detect amd64 or arm64, resolve one release, and verify its binary
against that release's `checksums.txt`. No Go, Docker or model key is required.

Use `--version vX.Y.Z` or `-Version vX.Y.Z` to pin a release. Choose another
destination with `--bin-dir` or `-BinDir`. Defaults are `~/.local/bin` on
Linux/macOS and `%LOCALAPPDATA%\Commander\bin` on Windows.

An identical installed binary is kept. Replacing a different binary requires
`--force` or `-Force`; failed downloads or checksum checks preserve the existing
file. Installers do not change your shell profile, persistent `PATH`, Commander
state or MCP client settings, and do not start services.

Run `commander --all-in-one` for Playground, or follow the
[coding-agent connection guide](../apps/playground/README.md#connect-a-coding-agent)
when using a release that includes `start` and `setup`.

Published installers will be root release assets named `install.sh` and
`install.ps1`; existing releases are not modified. Until a release includes them,
use the checked-out scripts or the [direct binary downloads](../README.md#binary).
