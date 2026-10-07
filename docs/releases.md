# Releasing Commander

Commander uses one version for the repository and all
[workspace modules](../go.work). `v0.1.0` is the planned first version, not an
existing release. Current `v0.0.0` requirements are placeholders and block
release validation until maintainers update them.

## Version and tags

Choose a new `v0.X.Y` or `v1.X.Y` without prerelease or build suffixes;
`v0.0.0` is reserved. Publishing creates the root tag, such as `v0.1.0`, and
a tag for each module in the form `<module-directory>/<version>`, such as
`framework/v0.1.0` and `providers/fs/v0.1.0`, at the same validated commit.
The root tag identifies the overall release; there is still no root `go.mod`.
Nested tags follow Go's [subdirectory tag rules](https://go.dev/ref/mod#vcs-version).
Versions from v2 require module-path changes and are outside this workflow.

## Prepare and validate

Update every internal Commander requirement in each `go.mod` to the chosen
version, including indirect requirements, and update matching version-specific
local mappings in `go.work`. Review and commit these changes through the normal
development process. The workflows do not edit dependency files for you.

Run the [release tests](../.github/workflows/test-release.yml) manually with that
version, or run their Go test from the repository root:

```sh
RELEASE_VERSION=v0.1.0 go test -count=1 -failfast -timeout=10m -v ./.github/tests/release_test.go
```

The check validates module paths, rejects `go.mod` replacements/exclusions,
requires known internal dependencies at the release version, checks workspace
mappings, and builds, vets and compiles tests for every workspace module.
It validates the workspace; it does not validate standalone module archives or
remote availability, run runtime tests, or start service/browser suites.

Run the relevant normal [test workflows](../.github/workflows/), including
service suites, and land the tested commit on the default branch.

## Publish

In GitHub Actions, open [Make release](../.github/workflows/make-release.yml),
choose the default branch, enter the version and select **Run workflow**.
This runs the reusable release tests first, then pushes the root and all module
tags atomically at that run's exact commit SHA. Publishing is allowed only from
the default branch. If any tag exists, publication fails without overwriting
tags or publishing a partial set.

Use the separate release-test workflow for validation without publication.
Normal CI must pass for the release commit. Publishing creates tags; it does
not publish binaries, deployment images or
[frontend assets](../apps/admin/ui/README.md#native-server).
