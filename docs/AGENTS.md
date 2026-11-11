# Working on documentation

Inherit the [repository guide](../AGENTS.md). [README.md](README.md) routes readers
to the appropriate usage or component guide.

- Keep the root README short: positioning, a starting point and documentation links.
- [installation.md](installation.md) owns native installers, direct downloads and
  source builds. [deployment.md](deployment.md) owns Docker, Compose, service
  hosting, deployment credentials and state. Link to component guides for their
  detailed configuration instead of copying option inventories.
- Distinguish released downloads from features prepared for an upcoming version.
  Do not advertise installer release assets before publication.
- Preserve credential, persistence, backup and teardown instructions when moving
  deployment content. Keep historical release notes intact.
- Update incoming links when moving sections. Check local paths, anchors and
  README/AGENTS pairing; documentation-only changes need no application tests.
