# Agent Guidelines

## Go

Never define variables inside `if` conditions. Always declare variables on a separate line before the conditional check.

```go
// Wrong
if err := something(); err != nil { ... }

// Wrong
if val, ok := something.(string); ok { ... }

// Right
err := something()
if err != nil { ... }

// Right
val, ok := something.(string)
if ok { ... }
```

If you modify `pkg/config.Config` or any struct referenced from it, always run `make gen-config` before finishing the task.

The Go build tag `unit` is meant for files that contain helpers for unit tests. It should never be included in a Go test file (ending in `_test.go`).

## JavaScript, TypeScript, JSX, and TSX

### Package management

The client project uses **pnpm** (NOT `npm`) for all package operations in the `client/` directory.

### Braces

Always use braces `{}` around control flow bodies — no single-line statements.

```js
// Wrong
if (foo) return false

// Right
if (foo) {
    return false
}
```

## Comments (all languages)

- One sentence per line; do not wrap to a max line length
- No trailing period on single-line comments

```go
// Wrong — wrapped mid-sentence
// This function performs the main validation logic. It checks
// the input against the schema and returns an error if the
// input is invalid.

// Wrong — trailing period on single-line comment
// Validate the input.

// Right
// This function performs the main validation logic
// It checks the input against the schema and returns an error if the input is invalid

// Right
// Validate the input
```

## UI

All clickable `<button>` elements must expose `cursor: pointer` when enabled.

## Running tests

Always pass `-tags unit` when running Go tests — several test helpers are guarded by that build tag, so tests will fail to compile without it.

```sh
go test -tags unit ./...
```

## Running the linter

The linter is pinned to a specific version (included in `.golangci-lint-version`), which must be installed from the pre-compiled binary: builds from package managers are compiled with an older Go and refuse this module's Go version.

```sh
VERSION="2.x.x" # From .golangci-lint-version
PLATFORM="linux-amd64"
curl -sSL https://github.com/golangci/golangci-lint/releases/download/v${VERSION}/golangci-lint-${VERSION}-${PLATFORM}.tar.gz | tar -xz -C /tmp
install /tmp/golangci-lint-${VERSION}-${PLATFORM}/golangci-lint /usr/local/bin
make lint
```

## Git

Do not stage or unstage changes unless the user explicitly asks you to.
