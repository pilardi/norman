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

.PHONY: help generate build test test-race test-short clean

CODEGEN := ~/go/bin/oapi-codegen
DIST    := dist

help:
	@echo "Norman Build System"
	@echo ""
	@echo "Targets:"
	@echo "  help        - Show this help message"
	@echo "  generate    - Generate protocol types, client library, and server handlers from api-spec.yaml"
	@echo "  build       - Run generate + compile all packages (binaries in $(DIST)/)"
	@echo "  test        - Run all unit tests with coverage report"
	@echo "  test-race   - Run all unit tests with race detector"
	@echo "  test-short  - Run short tests only (skip slow/integration)"
	@echo "  clean       - Remove generated files and binaries in dist/"

generate:
	@echo "Generating protocol types..."
	$(CODEGEN) -package protocol -generate types api-spec.yaml > pkg/protocol/types.go
	@echo "Generating client library..."
	$(CODEGEN) -package protocol -generate client api-spec.yaml > pkg/protocol/client.go
	@echo "Generating server handlers..."
	$(CODEGEN) -package api -generate server api-spec.yaml > internal/api/handlers.go
	@echo "Fixing cross-package type references in handlers..."
	sed -i '/^import (/a\\t"github.com/norman/norman/pkg/protocol"' internal/api/handlers.go
	sed -i 's/GetConfigParams\b/protocol.GetConfigParams/g; s/ListTasksParams\b/protocol.ListTasksParams/g; s/TailTaskLogParams\b/protocol.TailTaskLogParams/g; s/SessionHistoryParams\b/protocol.SessionHistoryParams/g' internal/api/handlers.go
	@echo "Regenerated code; compiling..."

build: generate
	go mod tidy
	mkdir -p $(DIST)
	go build -o $(DIST)/norman ./cmd/norman
	go build -o $(DIST)/norman-server ./cmd/norman-server

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
