# Working on release documentation

Inherit the [repository guide](../../AGENTS.md). [README.md](README.md) owns the
release instructions and version index; `note-vX.Y.Z.md` files own versioned notes.

- Preserve historical notes when adding a version, and link its file from the index.
- Current releases require exact `## Commander <version>`,
  `## Commander builder agent <version>` and `## Commander spawner agent <version>`
  headings. Keep package notes self-contained; the publication workflow extracts
  those sections through [release-notes.sh](../../.github/scripts/release-notes.sh).
- Use absolute links in note bodies because they are also GitHub release
  descriptions. Keep README links relative to this directory.
- [Release readiness](../../.github/tests/release_test.go) validates the matching
  version file before publication. Never substitute the README index for that file.

For documentation moves, check incoming links, anchors and README/AGENTS pairing.
Validate note selection from the repository root with:

```sh
RELEASE_VERSION=v0.5.0 go test -count=1 -timeout=1m -run '^TestReleaseNotes$' ./.github/tests/release_test.go
```
