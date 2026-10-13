.PHONY: test
test:
	go test -tags unit ./...

.PHONY: test-race
test-race:
	CGO_ENABLED=1 go test -race -tags unit ./...

.PHONY: lint
lint:
	golangci-lint run

# The Go code embeds dashboard/dist, so the dashboard must be built before building, linting or running the Go code
.PHONY: dashboard
dashboard:
	cd dashboard && pnpm install --frozen-lockfile && pnpm build

.PHONY: build
build: dashboard
	CGO_ENABLED=0 go build -o .bin/ddup ./cmd/ddup

# Runs ddup with the "dashboarddev" tag, which serves static sample data
# It still needs a valid config file: set DDUP_CONFIG to its path
.PHONY: run-dev
run-dev: dashboard
	go run -tags dashboarddev ./cmd/ddup

.PHONY: lint-dashboard
lint-dashboard:
	cd dashboard && pnpm install --frozen-lockfile && pnpm type-check && pnpm lint && pnpm format:check

# Needs a Playwright browser: run "cd dashboard && pnpm exec playwright install chromium" once
# Extra arguments for Playwright can be passed with E2E_ARGS
.PHONY: test-e2e
test-e2e:
	cd dashboard && pnpm install --frozen-lockfile && pnpm test:e2e $(E2E_ARGS)

# Runs the checks in the same order as CI, followed by the dashboard checks that CI doesn't run (lint, format and the type check of the scripts)
.PHONY: check
check:
	go tool gomod-age
	$(MAKE) dashboard
	$(MAKE) lint
	$(MAKE) test-race
	$(MAKE) lint-dashboard
	$(MAKE) test-e2e
