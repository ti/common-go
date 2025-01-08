all: clean build
clean:
	 rm -rf ./pkg
build: SHELL:=/bin/bash
build:
	mkdir -p ./pkg/go && mkdir -p ./pkg/python && mkdir -p ./pkg/descriptor
	docker run --rm -v $(shell pwd)/pkg/go:/build/go  -v $(shell pwd)/pkg/python:/build/python -v $(shell pwd):/build/proto nanxi/protoc
	docker run --rm  -v $(shell pwd)/pkg/descriptor:/build/descriptor -v $(shell pwd):/build/proto nanxi/protoc /build/build_descriptor.sh
