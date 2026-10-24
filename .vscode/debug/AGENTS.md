# Working on container debugging

- [compose.yaml](compose.yaml) overlays the root Compose file. Paths resolve from
  that first file: keep the build context at the repository root and point its
  Dockerfile to `.vscode/debug/Dockerfile`. Preserve the existing project/volumes
  so debugging cannot start a second controller against the same database.
- [Dockerfile](Dockerfile) builds unoptimized native binaries with `/src` paths;
  [launch.json](../launch.json) maps the workspace to `/src`. Keep Delve versions
  compatible with the workspace Go toolchain and attach ports on loopback.
- Keep secrets-key and mcp-credentials initializers on the corresponding debug
  images. Preserve key mounts, explicit credential overrides and startup exports.
  Delve is PID 1; credential retrieval must remain an explicit user action.
- Verify the merged configuration with `docker compose -f compose.yaml -f
  .vscode/debug/compose.yaml config --quiet`. Build/attach checks need Docker and
  VS Code; report them separately if unavailable. Keep the README focused on
  starting, attaching, rebuilding, credentials and returning to normal images.
