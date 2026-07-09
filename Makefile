VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build test vet fmt lint staticcheck govulncheck gosec generate clean

build:
	go build $(LDFLAGS) -o bin/terraform-provider-taipan .

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

lint: vet staticcheck
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

# Static analysis beyond go vet. Install: go install honnef.co/go/tools/cmd/staticcheck@latest
staticcheck:
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed; skipping (go install honnef.co/go/tools/cmd/staticcheck@latest)"

# Install: go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck:
	@command -v govulncheck >/dev/null 2>&1 && govulncheck ./... || echo "govulncheck not installed; skipping (go install golang.org/x/vuln/cmd/govulncheck@latest)"

# Install: go install github.com/securego/gosec/v2/cmd/gosec@latest
gosec:
	@command -v gosec >/dev/null 2>&1 && gosec -quiet ./... || echo "gosec not installed; skipping (go install github.com/securego/gosec/v2/cmd/gosec@latest)"

# Regenerate docs from schema descriptions (requires tfplugindocs).
generate:
	@command -v tfplugindocs >/dev/null 2>&1 && tfplugindocs generate || echo "tfplugindocs not installed; skipping (go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@latest)"

clean:
	rm -rf bin
