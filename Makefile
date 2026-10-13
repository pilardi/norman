# Norman — Build & Code Generation

.PHONY: generate build test clean

CODEGEN := ~/go/bin/oapi-codegen

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
	rm -f pkg/protocol/types.go pkg/protocol/client.go internal/api/handlers.go
