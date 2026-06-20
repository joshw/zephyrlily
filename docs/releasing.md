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