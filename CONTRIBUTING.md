# Contributing

Open an issue before starting a large change to agree on scope.
Keep changes focused, test changed behavior and minimize dependencies.

Use the Go version specified in `go.mod`. Before submitting a pull request, run:

```sh
go test -race ./...
go vet ./...
python3 -m unittest discover test/tailnet-preview
```

Keep credentials, local state and private tailnet information out of changes and logs.

## Releasing

Run `scripts/release.sh X.Y.Z` on an up-to-date `main`. It runs tests, sets and commits the version in `gemini-extension.json`, then opens your editor for notes that become the GitHub release text.

The script prints the push command but never pushes.

Bump the middle version number for new settings or changed behavior, the last for fixes and wording.
