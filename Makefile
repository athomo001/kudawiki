# Mismos pasos en local y en CI (.github/workflows/ci.yml).
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
GOSEC := github.com/securego/gosec/v2/cmd/gosec@v2.30.0

.PHONY: ui build test lint audit load e2e

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

# Bloquea con vulnerabilidades alcanzables en Go o en dependencias de producción de la UI, y con
# cualquier hallazgo alto de gosec (SPEC §21.2.2). Los revisados llevan `#nosec <regla> -- motivo`
# en la línea; los medios y bajos se revisan en la auditoría interna (docs/seguridad/).
audit:
	go run $(GOVULNCHECK) ./...
	go run $(GOSEC) -quiet -severity high -exclude-dir=investigacion ./...
	cd ui && pnpm audit --prod

# Prueba de carga (SPEC §9.5): guarda 50 000 páginas y busca por la API. Tarda minutos; el
# avance sale en vivo y el resultado queda en docs/rendimiento.md aunque pase.
load:
	KUDAWIKI_LOAD=1 go test -run TestSearchLoad -v -count=1 -timeout 0 ./internal/httpapi/

# Flujos principales en el navegador (SPEC §17): Playwright con el Chrome del equipo contra el
# binario real, en una carpeta temporal (ui/e2e/server.ts). Compila el binario: tarda un poco.
e2e: ui
	cd ui && pnpm exec playwright test
