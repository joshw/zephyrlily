Release process:

Install Prerequisites:

```
brew install go goreleaser
set GITHUB_TOKEN environment variable to a token
```

Test locally, without releasing:

```
goreleaser release --snapshot --clean
```

Releasing for real:
```
git tag v<version>
git push --tags
```

Goreleaser will be run automatically as a github action and will create the release and
upload the per-OS/arch archives plus a `checksums.txt`.

## Install paths produced by a release

Each release publishes three ways to install, all configured to stay in sync
with `.goreleaser.yaml` so there is nothing to update per release:

1. **curl installer** — after GoReleaser finishes, the release workflow runs
   [binstaller](https://github.com/binary-install/binstaller) (`binst`) to
   generate `install.sh` from [`.config/binstaller.yml`](../.config/binstaller.yml),
   embedding the checksums GoReleaser just published, and attaches it to the
   release. Users install with:
   ```bash
   curl -sSL https://github.com/joshw/zephyrlily/releases/latest/download/install.sh | sh
   ```
2. **Homebrew** — GoReleaser pushes a cask to the `joshw/homebrew-tap` repo.
3. **Scoop** — GoReleaser pushes a manifest to the `joshw/scoop-bucket` repo.

### One-time prerequisites for Homebrew / Scoop

Publishing to the tap repositories requires permissions the default
`GITHUB_TOKEN` does not have, so a one-time setup is needed:

- Create the repos `joshw/homebrew-tap` and `joshw/scoop-bucket`.
- Create a Personal Access Token with write access to both (classic `repo`
  scope, or a fine-grained token with contents:write on those repos) and add it
  as the Actions secret `TAP_GITHUB_TOKEN` on this repository.

### Regenerating the binstaller config

`.config/binstaller.yml` only needs to be regenerated if the GoReleaser archive
naming or binary set changes:

```bash
go install github.com/binary-install/binstaller/cmd/binst@latest
binst init --source=goreleaser --file=.goreleaser.yaml \
  --repo joshw/zephyrlily --name zephyrlily -o .config/binstaller.yml
# re-add the asset.binaries entry mapping the archive's `zlily` binary, then:
binst check --config .config/binstaller.yml   # validates against the latest release
```

Note the config's `asset.binaries` maps the binary `zlily` inside the
`zephyrlily_*` archives — `binst init` does not infer this automatically because
the project name (`zephyrlily`) differs from the binary name (`zlily`).

## Dev builds

To try a build without cutting a release, run the **Dev build** workflow from
the Actions tab and pick a branch. It runs GoReleaser in snapshot mode, so it
produces the same archives a release would — same before-hooks, same ldflags,
same embedded browser client — and attaches them to the run as artifacts.

Nothing is tagged, no GitHub release is created, and the Homebrew and Scoop taps
are untouched: snapshot mode skips announce, publish and validate outright.

The version comes out as `<next>-SNAPSHOT-<sha>`, e.g.
`0.12.0-SNAPSHOT-c436ebe`, so a dev binary is never mistaken for a release.

Two options on the run form:

- **all** — every platform, as a release does. ~390 MB of artifacts, about a
  minute of build time.
- **linux-amd64-only** — one binary, for when the point is to try the change
  rather than to check every platform still compiles.

Artifacts are kept for 14 days. They carry the s.u13.net credential in the same
way released binaries do, and only people with write access can start the
workflow.

### Handing a dev build to someone else

GitHub only serves workflow artifacts to a logged-in account, even for a public
repository, so an artifact link is no use to someone without one. The **publish**
option (on by default) also puts the archives on a rolling prerelease at the tag
`dev`, and release assets on a public repository are served to anyone:

    https://github.com/joshw/zephyrlily/releases/download/dev/zephyrlily_Linux_x86_64.tar.gz

The tag is replaced by every dev build, so that URL is stable and always points
at the most recent one. It is marked prerelease, so the newest real release
stays the one GitHub calls *Latest*, and `dev` does not match the `v*` pattern
that starts the release workflow — publishing a dev build cannot cut a release
or touch the Homebrew and Scoop taps.

Turn **publish** off for a build you would rather not put a public link on.
It is skipped automatically for a single-platform build, since a `dev` release
holding one platform would mislead anyone who found it.
