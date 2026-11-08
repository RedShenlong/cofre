GO ?= go
BIN := cofre

.PHONY: build test vet install uninstall clean

build: ## Compila el binario
	$(GO) build -trimpath -ldflags="-s -w" -o $(BIN) .

test: ## Ejecuta las pruebas
	$(GO) test -race ./...

vet: ## Análisis estático
	$(GO) vet ./...
	@test -z "$$(gofmt -l .)" || (echo "Archivos sin formatear:"; gofmt -l .; exit 1)

install: ## Instala el binario y el servicio de usuario
	./install.sh

uninstall: ## Desinstala (los datos se conservan)
	./uninstall.sh

clean:
	rm -f $(BIN)
