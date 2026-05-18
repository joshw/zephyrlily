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
goreleaser release --clean
```