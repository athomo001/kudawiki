# Mismos pasos en local y en CI (.github/workflows/ci.yml).
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

.PHONY: ui build test lint audit

ui:
	cd ui && pnpm install --frozen-lockfile && pnpm check && pnpm build

build: ui
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/kudawiki ./cmd/kudawiki

test: ui
	go test -race ./...

lint:
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...
	go run $(STATICCHECK) ./...

# Bloquea con vulnerabilidades alcanzables en Go o en dependencias de producción de la UI (SPEC §21.2).
audit:
	go run $(GOVULNCHECK) ./...
	cd ui && pnpm audit --prod
