all: clean build

clean:
	rm -rf ./pkg

# New buf-based build (recommended)
build:
	@echo "Building with buf..."
	@mkdir -p ./pkg/go ./pkg/python ./pkg/descriptor
	buf generate

# Legacy Docker-based build (deprecated, kept for reference)
build-docker: SHELL:=/bin/bash
build-docker:
	@echo "Building with Docker (legacy)..."
	@mkdir -p ./pkg/go ./pkg/python ./pkg/descriptor
	docker run --rm -v $(shell pwd)/pkg/go:/build/go -v $(shell pwd)/pkg/python:/build/python -v $(shell pwd):/build/proto nanxi/protoc
	docker run --rm -v $(shell pwd)/pkg/descriptor:/build/descriptor -v $(shell pwd):/build/proto nanxi/protoc /build/build_descriptor.sh

# Install buf (if not installed)
install-buf:
	@echo "Installing buf..."
	@go install github.com/bufbuild/buf/cmd/buf@latest

# Lint proto files
lint:
	buf lint

# Format proto files
format:
	buf format -w

# Check for breaking changes
breaking:
	buf breaking --against '.git#branch=main'

.PHONY: all clean build build-docker install-buf lint format breaking
