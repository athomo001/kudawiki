# Mismos pasos en local y en CI (.github/workflows/ci.yml).
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

.PHONY: ui build test lint audit load

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

# Prueba de carga (SPEC §9.5): guarda 50 000 páginas y busca por la API. Tarda minutos; el
# avance sale en vivo y el resultado queda en docs/rendimiento.md aunque pase.
load:
	KUDAWIKI_LOAD=1 go test -run TestSearchLoad -v -count=1 -timeout 0 ./internal/httpapi/
