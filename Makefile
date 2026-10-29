# Norman — Build & Code Generation
#
# Targets:
#   help      Show this help message
#   generate  Generate protocol types, client library, and server handlers from api-spec.yaml
#   build     Run generate + compile all packages (binaries in dist/)
#   test      Run all unit tests with coverage report
#   test-race Run all unit tests with race detector
#   test-short Run short tests only (skip slow/integration)
#   clean     Remove generated files and binaries in dist/

.PHONY: help generate build rebuild test test-race test-short clean generate-docs generate-docs-html

CODEGEN := ~/go/bin/oapi-codegen
DIST    := dist

help:
	@echo "Norman Build System"
	@echo ""
	@echo "Targets:"
	@echo "  help        - Show this help message"
	@echo "  generate    - Generate protocol types, client library, and server handlers from api-spec.yaml"
	@echo "  build       - Run generate + compile all packages (binaries in $(DIST)/)"
	@echo "  rebuild     - Clean + regenerate + build (safe sequential rebuild)"
	@echo "  test        - Run all unit tests with coverage report"
	@echo "  test-race   - Run all unit tests with race detector"
	@echo "  test-short  - Run short tests only (skip slow/integration)"
	@echo "  generate-docs - Generate API reference HTML from api-spec.yaml"

generate:
	@mkdir -p pkg/protocol internal/api
	@echo "Generating protocol types..."
	$(CODEGEN) -package protocol -generate types api-spec.yaml > pkg/protocol/types.go
	@echo "Generating client library..."
	$(CODEGEN) -package protocol -generate client api-spec.yaml > pkg/protocol/client.go
	@echo "Generating server handlers..."
	$(CODEGEN) -package api -generate server api-spec.yaml > internal/api/handlers.go
	@echo "Fixing cross-package type references in handlers..."
	sed -i '/^import (/a\\t"github.com/norman/norman/pkg/protocol"' internal/api/handlers.go
	sed -i 's/GetConfigParams\b/protocol.GetConfigParams/g; s/ListTasksParams\b/protocol.ListTasksParams/g; s/TailTaskLogParams\b/protocol.TailTaskLogParams/g; s/SessionHistoryParams\b/protocol.SessionHistoryParams/g; s/ListWorkspacesParams\b/protocol.ListWorkspacesParams/g' internal/api/handlers.go
	@echo "Regenerated code; compiling..."

build: generate
	go mod tidy
	mkdir -p $(DIST)
	go build -o $(DIST)/norman ./cmd/norman
	go build -o $(DIST)/norman-server ./cmd/norman-server
	go build -o $(DIST)/roger ./cmd/roger

rebuild:
	@$(MAKE) clean
	@$(MAKE) build

test:
	@echo "Running all tests with coverage..."
	go test -count=1 -coverprofile=coverage.out ./...
	@echo ""
	@go tool cover -func=coverage.out | tail -n +2 | awk '{printf "  %-40s %s\n", $$1, $$3}'
	@rm -f coverage.out

test-race:
	@echo "Running all tests with race detector..."
	go test -race -count=1 ./...

test-short:
	go test -short ./...

clean:
	rm -rf $(DIST)
	rm -f pkg/protocol/types.go pkg/protocol/client.go internal/api/handlers.go

generate-docs:
	@echo "Generating API reference HTML via Scalar..."
	go run ./cmd/docs-gen/ -spec api-spec.yaml -out docs/api-reference.html --title "Norman API Reference" --css ":root{--bg-color:#000000;--color-gold:#D4AF37;--color-red:#A52A2A;--color-silver:#E5E5E5;--color-charcoal:#1C1C1C}"

generate-docs-html: generate-docs
	@echo "Converting markdown files to HTML..."
	go run ./cmd/md2html/ --out docs/ docs/*.md
