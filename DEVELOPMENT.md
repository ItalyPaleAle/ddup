# Developing ddup

This guide explains how to build, run, test and check ddup on your machine.
The short version is `make check`, which runs everything CI runs.

## What you need

| Tool | Version |
| --- | --- |
| Go | The version in [`go.mod`](./go.mod) |
| Node.js | The version in [`.node-version`](./.node-version) |
| pnpm | The version in `packageManager` in [`dashboard/package.json`](./dashboard/package.json) |
| golangci-lint | The version in [`.golangci-lint-version`](./.golangci-lint-version) |

You can also run the dashboard commands without `make`, see [Working on the dashboard](#working-on-the-dashboard).

### Getting the tools

You can install the tools with any method you like.
If you use [mise](https://mise.jdx.dev), this runs a command with all the right versions (the versions below are the ones at the time of writing, so check the files above):

```sh
mise x node@24 pnpm@12.6.0 golangci-lint@2.14.0 -- make check
```

If you prefer to install them yourself:

- **Go**: follow [go.dev/doc/install](https://go.dev/doc/install).
- **Node.js** and **pnpm**: follow [nodejs.org](https://nodejs.org/en/download) and [pnpm.io/installation](https://pnpm.io/installation).
- **golangci-lint**: use the pre-compiled binary of the pinned version.
  Builds from package managers are often compiled with an older Go, and they refuse to lint this module.
  The `.golangci-lint-version` file already contains the leading `v`, so `${VERSION}` below is, for example, `v2.14.0` (and `${VERSION#v}` is `2.14.0`):

  ```sh
  VERSION="$(cat .golangci-lint-version)"
  PLATFORM="linux-amd64" # Change this for your OS and architecture
  curl -sSL "https://github.com/golangci/golangci-lint/releases/download/${VERSION}/golangci-lint-${VERSION#v}-${PLATFORM}.tar.gz" | tar -xz -C /tmp
  install "/tmp/golangci-lint-${VERSION#v}-${PLATFORM}/golangci-lint" /usr/local/bin
  ```

  Other platforms and install methods are listed in the [golangci-lint docs](https://golangci-lint.run/welcome/install/#binaries).

## How the repository is laid out

- `cmd/ddup`: the `main` package. `main_dashboarddev.go` is only built with the `dashboarddev` tag, and replaces the health checker with static sample data
- `pkg/`: the Go code (config, DNS providers, health checks, the HTTP server, metrics)
- `dashboard/`: the web dashboard, a React app built with Vite. `dashboard/e2e` has its Playwright tests
- `config.sample.yaml`: a sample config, documenting every option

## Building

The Go code embeds the compiled dashboard (`dashboard/dist`, see [`dashboard/dashboard.go`](./dashboard/dashboard.go)).
The folder is not in git, and without it Go can't compile the `dashboard` package, and commands that need it (for example `go build ./cmd/ddup`) fail with `pattern all:dist/*: no matching files found`.
So you always build the dashboard first, then Go.

```sh
make dashboard # Installs the dependencies and builds the dashboard into dashboard/dist
make build     # Builds the dashboard, then the ddup binary into .bin/ddup
```

You need to run `make dashboard` again after changing the dashboard, so the Go code embeds the new version.

## Running ddup

ddup needs a config file.
Copy [`config.sample.yaml`](./config.sample.yaml) to `config.yaml` in the root of the repository (git ignores it) and edit it, then run:

```sh
DDUP_CONFIG=config.yaml go run ./cmd/ddup
```

Run `make dashboard` first, so the dashboard is available.
The sample config uses a placeholder Cloudflare token, so with it ddup checks the sample endpoints (which don't exist) but can't update DNS records.
To try ddup for real, use a provider you control: see the [README](./README.md#configuration) for the options.

## Running the dashboard with sample data

If you only want to work on the dashboard, you don't need working endpoints or DNS credentials.
Build with the `dashboarddev` tag, and ddup serves static sample data (healthy, degraded and failing domains) instead of running health checks:

```sh
DDUP_CONFIG=config.sample.yaml make run-dev
```

This runs `make dashboard`, then `go run -tags dashboarddev ./cmd/ddup`.
ddup still loads the config file, so the server must be enabled in it: the sample config is fine.
Open <http://127.0.0.1:7401> (or the `bind` and `port` you set), or fetch the data with `curl http://127.0.0.1:7401/api/status`.
Stop it with `Ctrl+C`.

The "Check now" button works too, and waits 1.5 seconds before returning the same data.

## Working on the dashboard

The dashboard commands are in [`dashboard/package.json`](./dashboard/package.json), and you run them from the `dashboard` folder:

```sh
cd dashboard
pnpm install --frozen-lockfile
pnpm build         # tsc and vite build, outputs to dist/
pnpm type-check
pnpm lint
pnpm format:check
pnpm format        # Fixes the formatting
```

`pnpm dev` starts the Vite dev server with hot reload, but it **can't show the data** from a ddup server at the moment.
The dashboard asks for `/api/status` on the same origin it was loaded from, and the Vite config has no proxy, so `/api/status` on the dev server returns the HTML page rather than JSON.
Until that changes, rebuild with `make dashboard` and reload the page served by `make run-dev`.

## Tests

Go tests need the `unit` build tag.
It enables helpers that the tests share, and it must not be added to a test file (a file ending in `_test.go`).

```sh
make test      # go test -tags unit ./...
make test-race # The same, with the race detector (CGO_ENABLED=1, needs a C compiler)
```

CI runs the race detector, so run `make test-race` before sending a PR.
`go test ./...` fails to compile some packages (`cmd/ddup`, `pkg/server`) unless `dashboard/dist` exists, so run `make dashboard` first on a fresh clone.

### End-to-end tests

The dashboard has [Playwright](https://playwright.dev) tests in `dashboard/e2e`.
They build the dashboard, serve it with `vite preview`, and mock the API, so they don't need a ddup server.
They run in Chromium.

Install the browser once (CI does the same, with `--with-deps` to also install system libraries on Linux), then run the tests:

```sh
(cd dashboard && pnpm install --frozen-lockfile && pnpm exec playwright install chromium)
make test-e2e
```

Pass extra Playwright arguments with `E2E_ARGS`, for example `make test-e2e E2E_ARGS="--grep Check"`.
When a test fails, Playwright keeps its trace in `dashboard/test-results`.

## Linting

```sh
make lint           # golangci-lint run, needs dashboard/dist
make lint-dashboard # Type check, ESLint and Prettier on the dashboard
```

## Before you open a pull request

`make check` runs all of this, in this order:

1. `go tool gomod-age`: checks that the Go dependencies are old enough (see [`.gomod-age.json`](./.gomod-age.json))
2. `make dashboard`: builds the dashboard
3. `make lint`: golangci-lint
4. `make test-race`: Go tests with the race detector
5. `make lint-dashboard`: dashboard type check, ESLint and Prettier
6. `make test-e2e`: the Playwright tests

CI runs steps 1 to 4 in one job and step 6 in another.
The dashboard build in step 2 runs `tsc`, so CI type-checks the app, but it doesn't run `pnpm lint`, `pnpm format:check` or the extra `tsc -p scripts` of `pnpm type-check`.
Step 5 runs them, so please do too: it keeps the dashboard code consistent.
See [`.github/workflows/ci.yaml`](./.github/workflows/ci.yaml).

## Opening a pull request

- Keep each pull request focused on one change
- Write short commit messages in the imperative.
  The history mixes plain messages with [conventional commits](https://www.conventionalcommits.org) like `feat(dashboard): ...` and `fix(server): ...`; either is fine, and the second style is welcome for features and fixes
- **Sign your commits.**
  The repository requires signed commits: see GitHub's guide to [signing commits](https://docs.github.com/en/authentication/managing-commit-signature-verification/signing-commits)

Code style notes for Go, TypeScript and comments are in [`AGENTS.md`](./AGENTS.md), and apply to everyone, not only coding agents.
