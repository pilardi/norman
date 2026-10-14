# Norman — Build & Code Generation
#
# Targets:
#   help      Show this help message
#   generate  Generate protocol types, client library, and server handlers from api-spec.yaml
#   build     Run generate + compile all packages
#   test      Run all unit tests
#   clean     Remove generated files

.PHONY: help generate build test clean

CODEGEN := ~/go/bin/oapi-codegen

help:
	@echo "Norman Build System"
	@echo ""
	@echo "Targets:"
	@echo "  help      - Show this help message"
	@echo "  generate  - Generate protocol types, client library, and server handlers from api-spec.yaml"
	@echo "  build     - Run generate + compile all packages"
	@echo "  test      - Run all unit tests"
	@echo "  clean     - Remove generated files"

generate:
	@echo "Generating protocol types..."
	$(CODEGEN) -package protocol -generate types api-spec.yaml > pkg/protocol/types.go
	@echo "Generating client library..."
	$(CODEGEN) -package protocol -generate client api-spec.yaml > pkg/protocol/client.go
	@echo "Generating server handlers..."
	$(CODEGEN) -package api -generate server api-spec.yaml > internal/api/handlers.go
	@echo "Fixing cross-package type references in handlers..."
	sed -i '/^import (/a\\t"github.com/norman/norman/pkg/protocol"' internal/api/handlers.go
	sed -i 's/ListTasksParams\b/protocol.ListTasksParams/g; s/TailTaskLogParams\b/protocol.TailTaskLogParams/g' internal/api/handlers.go
	@echo "Done. Run 'make build' to verify compilation."

build: generate
	go mod tidy
	go build ./...

test:
	go test ./...

clean:
	find . -name '*.exe' -delete 2>/dev/null || true
	find . -type f -name 'norman' -delete 2>/dev/null || true
	find . -type f -name 'norman-server' -delete 2>/dev/null || true
