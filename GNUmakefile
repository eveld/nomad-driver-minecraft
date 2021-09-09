PLUGIN_BINARY=example/files/minecraft
export GO111MODULE=on

default: build

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf ${PLUGIN_BINARY}

build:
	go build -o ${PLUGIN_BINARY} .

dev: build
	shipyard taint nomad_cluster.dev
	shipyard run example