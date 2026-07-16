GOVULNCHECK_VERSION ?= v1.6.0
ACTIONLINT_VERSION ?= v1.7.7

.PHONY: build test test-packages check release-check

build:
	mkdir -p bin
	go build -o bin/tmh ./cmd/tmh

test:
	go test -race -timeout 2m ./...

test-packages:
	npm test --prefix npm
	sh tests/packages.test.sh
	sh tests/release-verification.test.sh

check: test test-packages
	@files="$$(gofmt -l $$(find . -type f -name '*.go'))"; \
		test -z "$$files" || { printf 'Go files need gofmt:\n%s\n' "$$files" >&2; exit 1; }
	go vet ./...
	zsh -n internal/shellinit/scripts/tmh.zsh
	bash -n internal/shellinit/scripts/tmh.bash
	@if command -v fish >/dev/null 2>&1; then fish --no-config --no-execute internal/shellinit/scripts/tmh.fish; else printf 'Skipping Fish syntax check (fish not installed).\n'; fi
	sh -n install.sh scripts/release-lib.sh scripts/prepare-release-packages.sh scripts/render-homebrew-formula.sh scripts/verify-release-assets.sh scripts/verify-npm-package.sh scripts/verify-published-packages.sh tests/*.sh
	bash -n scripts/release.sh
	sh tests/install.test.sh
	sh tests/release-script.test.sh
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	goreleaser check
	go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) .github/workflows/*.yml

release-check:
	@test -n "$(VERSION)" || { printf 'VERSION is required (vMAJOR.MINOR.PATCH)\n' >&2; exit 2; }
	@version="$$(. ./scripts/release-lib.sh; release_normalize_version "$(VERSION)")"; \
		goreleaser release --snapshot --clean; \
		scripts/verify-release-assets.sh dist; \
		TMH_VERIFY_SNAPSHOT=1 scripts/prepare-release-packages.sh "$$version" dist dist/packages >/dev/null; \
		TMH_VERIFY_SNAPSHOT=1 scripts/verify-npm-package.sh "$$version" "dist/packages/allenreder-tmh-$${version#v}.tgz"; \
		ruby -c dist/packages/tmh.rb >/dev/null
