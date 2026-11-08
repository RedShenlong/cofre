GO ?= go
BIN := cofre

.PHONY: build test vet cross install uninstall clean

build: ## Compila el binario
	$(GO) build -trimpath -ldflags="-s -w" -o $(BIN) .

test: ## Ejecuta las pruebas
	$(GO) test -race ./...

vet: ## Análisis estático
	$(GO) vet ./...
	@test -z "$$(gofmt -l .)" || (echo "Archivos sin formatear:"; gofmt -l .; exit 1)

cross: ## Compila para Linux, macOS y Windows (amd64 y arm64) en dist/
	@mkdir -p dist
	@for os in linux darwin windows; do for arch in amd64 arm64; do \
	  ext=""; [ $$os = windows ] && ext=".exe"; \
	  echo "  $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags="-s -w" -o dist/cofre-$$os-$$arch$$ext . || exit 1; \
	done; done

install: ## Instala el binario y el servicio de usuario
	./install.sh

uninstall: ## Desinstala (los datos se conservan)
	./uninstall.sh

clean:
	rm -rf $(BIN) dist
