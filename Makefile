GOVULNCHECK_VERSION ?= v1.6.0

.PHONY: build test check

build:
	mkdir -p bin
	go build -o bin/tmh ./cmd/tmh

test:
	go test -race -timeout 2m ./...

check: test
	@files="$$(gofmt -l $$(find . -type f -name '*.go'))"; \
		test -z "$$files" || { printf 'Go files need gofmt:\n%s\n' "$$files" >&2; exit 1; }
	go vet ./...
	zsh -n shell/tmh.zsh
	zsh shell/tmh.zsh.test.zsh
	sh -n install.sh
	sh tests/install.test.sh
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	goreleaser check
