# Developer tasks for twclient. Mirrors the CI pre-check gate (SPEC §V97) so
# issues can be seen and fixed locally.
#
#   make tools           install staticcheck + govulncheck
#   make fmt             gofmt -w
#   make modernize       apply gopls modernizers in place (-fix)
#   make lint            staticcheck (must be clean — library carries no debt)
#   make vuln            govulncheck
#   make test            go test
#   make pre-check       the exact CI gate (fails on any leftover diff/finding)
#   make check           vet + lint + modernize-check + vuln + test

export GOFLAGS := -mod=mod

# Library Go files (the gitignored cmd/ harness is its own module, excluded).
GOFILES := $(shell find . -name '*.go' -not -path './cmd/*')
# Root-module packages (cmd/ is a separate module, excluded by go list ./...).
PKGS := $(shell go list ./...)

# gopls modernizer analyzer. Shipped as an internal package, so it is run via
# `go run` (cannot be `go install`ed from outside x/tools).
MODERNIZE := go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest

.PHONY: tools fmt tidy vet lint modernize modernize-check vuln test pre-check check

tools:
	go install honnef.co/go/tools/cmd/staticcheck@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

fmt:
	gofmt -w $(GOFILES)

tidy:
	go mod tidy

vet:
	go vet $(PKGS)

# staticcheck must be clean: the shipped library carries no legacy debt.
lint:
	staticcheck $(PKGS)

# Apply gopls modernizers in place (min/max, range-over-int, maps.Copy,
# bytes.Cut, WaitGroup.Go, …).
modernize:
	$(MODERNIZE) -fix ./...

# Report-only: non-zero exit if any modernizer fix is still pending (V97).
modernize-check:
	$(MODERNIZE) ./...

vuln:
	govulncheck $(PKGS)

test:
	go test -timeout 10m $(PKGS)

# Mirror of the CI pre-check gate: every mutating step must leave no diff and no
# finding. Run after `make tools`.
pre-check:
	@unformatted="$$(gofmt -l $(GOFILES))"; \
	  if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	$(MODERNIZE) ./...
	go vet $(PKGS)
	staticcheck $(PKGS)
	go mod tidy
	git diff --exit-code go.mod go.sum
	govulncheck $(PKGS)
	@echo "pre-check OK"

check: vet lint modernize-check vuln test

# E2E: docker harness (DDNet sixup + vanilla teeworlds 0.7) + client parity tests
# (V114, T132/T137). The test runs INSIDE the compose network and reaches the
# servers by service name (ddnet/teeworlds7) — host UDP port forwarding is
# unreliable on Docker Desktop (macOS), so container-to-container UDP is used.
# The Go version must match go.mod (1.26).
.PHONY: e2e
e2e:
	docker compose -f e2e/docker-compose.yml up -d --build
	docker run --rm --network tw-e2e_default -v "$(CURDIR)":/src -w /src \
	  -e TW_E2E=1 \
	  -e TW_E2E_DDNET_06=ddnet:8303 \
	  -e TW_E2E_DDNET_07=ddnet:8303 \
	  -e TW_E2E_VANILLA_07=teeworlds7:8303 \
	  golang:1.26 go test -tags e2e -timeout 10m -run TestE2E ./e2e/ -v ; \
	status=$$? ; \
	docker compose -f e2e/docker-compose.yml down ; \
	exit $$status
