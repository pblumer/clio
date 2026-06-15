# cliostore — Build- und Entwicklungs-Tasks
#
# Versionsstring: git-Tag/Commit, via -ldflags ins Binary eingebettet.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PKG     := ./cmd/cliostore
BIN     := cliostore
DIST    := dist

# Zielplattformen für die Cross-Builds (Single-Binary, statisch gelinkt).
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: all build run test race bench cover vet fmt fmt-check lint dist package docker clean smoke postman-gen

all: lint test build

## build: lokales Binary bauen
build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

## run: lokal starten (CLIO_API_TOKEN muss gesetzt sein)
run: build
	./$(BIN)

## test: alle Tests
test:
	go test ./...

## race: Tests mit Race-Detector
race:
	go test -race ./...

## smoke: Server starten + Postman-Collection per Newman ausfuehren (braucht npx)
smoke:
	./scripts/smoke.sh

## postman-gen: Postman-Collection aus der OpenAPI-Spec neu generieren (braucht npx)
postman-gen:
	npx --yes openapi-to-postmanv2 \
		-s internal/apidocs/openapi.yaml \
		-o postman/clio.postman_collection.json -p
	@echo "Hinweis: Tests/Variablen werden beim Generieren NICHT erzeugt —"
	@echo "die gepflegte Collection enthaelt zusaetzlich pm.test-Skripte."

## bench: Store-Benchmarks
bench:
	go test -run='^$$' -bench=. -benchmem ./internal/store/

## cover: Coverage (paketübergreifend) als Gesamtwert
cover:
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

## vet / fmt-check: statische Prüfungen (wie in CI)
vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Nicht gofmt-konform:"; echo "$$unformatted"; exit 1; \
	fi

lint: fmt-check vet

## dist: statische Single-Binaries für alle Plattformen nach $(DIST)/
dist: clean-dist
	@mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=; [ "$$os" = "windows" ] && ext=.exe; \
		out=$(DIST)/$(BIN)_$${os}_$${arch}$$ext; \
		echo "build $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags "$(LDFLAGS)" -o $$out $(PKG) || exit 1; \
	done
	@echo "fertig: $(DIST)/"

## package: Release-Archive (.tar.gz/.zip) + checksums.txt aus den dist-Binaries
package: dist
	@cd $(DIST) && rm -f checksums.txt; \
	for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=; [ "$$os" = "windows" ] && ext=.exe; \
		stage=$(BIN)_$(VERSION)_$${os}_$${arch}; \
		mkdir -p $$stage; \
		cp $(BIN)_$${os}_$${arch}$$ext $$stage/$(BIN)$$ext; \
		cp ../LICENSE ../README.md $$stage/; \
		if [ "$$os" = "windows" ]; then \
			zip -qr $$stage.zip $$stage; \
		else \
			tar -czf $$stage.tar.gz $$stage; \
		fi; \
		rm -rf $$stage $(BIN)_$${os}_$${arch}$$ext; \
	done; \
	if command -v sha256sum >/dev/null 2>&1; then \
		sha256sum $(BIN)_$(VERSION)_*.tar.gz $(BIN)_$(VERSION)_*.zip > checksums.txt; \
	else \
		shasum -a 256 $(BIN)_$(VERSION)_*.tar.gz $(BIN)_$(VERSION)_*.zip > checksums.txt; \
	fi; \
	echo "fertig: $(DIST)/*.tar.gz, *.zip, checksums.txt"

## docker: Image bauen
docker:
	docker build --build-arg VERSION=$(VERSION) -t $(BIN):$(VERSION) -t $(BIN):latest .

clean-dist:
	@rm -rf $(DIST)

clean: clean-dist
	@rm -f $(BIN) coverage.out
