# Install Commander

Choose [native installers](#native-installers), [direct downloads](#direct-downloads)
or a [source build](#build-from-source). For Containers, Compose and Kubernetes,
use the [deployment guide](deployment.md).

Background hosting and generic MCP setup require v0.5.0 or later, or a build of
this checkout. Earlier releases do not include these commands.

## Native installers

From a repository checkout, preview the released binary and destination before
installing. The scripts install release assets, not the checkout's source changes.

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

The scripts select amd64 or arm64, resolve one release, and verify its binary
against that release's `checksums.txt`. No Go, Docker or model key is required
to install the executable.

Use `--version vX.Y.Z` or `-Version vX.Y.Z` to pin a release, and `--bin-dir` or
`-BinDir` for another destination. Defaults are `~/.local/bin` on Linux/macOS and
`%LOCALAPPDATA%\Commander\bin` on Windows.

An identical binary is kept. Replacing a different binary requires `--force`
or `-Force`. Failed downloads or checksum checks preserve existing files.
Installers do not change shell profiles, persistent `PATH`, Commander state or
MCP client settings, and do not start services.

Check the selected release's asset list before downloading `install.sh` or
`install.ps1`. If that release does not include the scripts, use the checked-out
scripts or direct downloads below.

## Direct downloads

Download the matching executable from [Releases](https://github.com/defos-ai/commander/releases/latest).
Linux and macOS need executable permission; Windows filenames end in `.exe`.

| Platform | Asset |
| --- | --- |
| Linux amd64 | `commander_linux_amd64` |
| Linux arm64 | `commander_linux_arm64` |
| macOS Intel | `commander_darwin_amd64` |
| macOS Apple silicon | `commander_darwin_arm64` |
| Windows amd64 | `commander_windows_amd64.exe` |
| Windows arm64 | `commander_windows_arm64.exe` |

Linux amd64:

```sh
curl -fsSL https://github.com/defos-ai/commander/releases/latest/download/commander_linux_amd64 -o commander
```

macOS Apple silicon:

```sh
curl -fsSL https://github.com/defos-ai/commander/releases/latest/download/commander_darwin_arm64 -o commander
```

Windows amd64:

```powershell
Invoke-WebRequest -UseBasicParsing https://github.com/defos-ai/commander/releases/latest/download/commander_windows_amd64.exe -OutFile commander.exe
```

Before running, compare the binary's SHA-256 with the entry for its original
asset name in that release's `checksums.txt`. Use `sha256sum commander` on Linux,
`shasum -a 256 commander` on macOS, or `Get-FileHash .\commander.exe -Algorithm SHA256`
on Windows. Use `releases/download/<version>/<asset>` to pin a version.

On Linux/macOS, finish with `chmod +x commander`. Continue with
[running Commander](#run-commander).

## Build from source

With the Go version in [go.work](../go.work), run from the repository root:

```sh
go build -o commander ./apps/playground/cmd/commander
./commander --all-in-one
```

For Windows, use `go build -o commander.exe ./apps/playground/cmd/commander`.
The terminal runs without Node or frontend assets; missing Admin assets produce
a notice without stopping the local hosts. To include Admin assets, follow the
[bundled UI build](../apps/playground/README.md#build-from-source).

Native agents use tools installed on the host. Missing tools and secrets appear
in agent readiness; see [native dependencies](../apps/playground/README.md#build-from-source).

## Run Commander

Open Playground:

```sh
./commander --all-in-one
```

On Windows, run `.\commander.exe --all-in-one`. Choose a model, enter its API key,
and ask **Create a Hacker News digest**. The mission installs example-agent when
needed and saves a reading list as Markdown and JSON.

Admin starts automatically; use `/admin` for its link and `/credentials` for the
API login. If Admin cannot start, the other hosts keep running; `/admin serve`
retries after you fix the issue. State, settings and chat history are saved under
`~/.commander`; `--state-dir PATH` selects another profile and `--temporary`
uses disposable local state. Keep the entire profile, including encryption keys,
when backing up or restoring it. See [saved state](../apps/playground/README.md#saved-state).

For background hosts without Playground or a model key, use `commander start`,
then `commander setup --json` to get verified connection details for your coding
agent. If its sandbox ends background processes after a tool command, keep
[`commander --all-in-one --headless`](../apps/playground/README.md#headless-all-in-one)
running in a long-lived tool session and run setup from another tool call with
the same state directory. After configuring the MCP client, verify it by calling
`list_missions` through that client.

Follow the [coding-agent connection guide](../apps/playground/README.md#connect-a-coding-agent)
and [background lifecycle guide](../apps/playground/README.md#background-local-hosts).
