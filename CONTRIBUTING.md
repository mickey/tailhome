# Contributing

Open an issue before starting a large change so we can agree on scope.
Keep changes focused, add tests for changed behavior, and keep dependencies minimal.

Use the Go version specified in `go.mod`. Before submitting a pull request, run:

```sh
go test -race ./...
go vet ./...
```

Do not include credentials, local state, or private tailnet information in changes or logs.
