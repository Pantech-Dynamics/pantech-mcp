# make build     the server for this machine, in ./pantech-mcp
# make test      gofmt, vet and race tests
# make dist      every platform's release archive and SHA256SUMS in ./dist/<version>/
# make docker    the container image, tagged pantech-mcp:<version>

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/Pantech-Dynamics/pantech-mcp/mcp.Version=$(VERSION:v%=%)
PLATFORMS := darwin_arm64 darwin_amd64 linux_amd64 linux_arm64 windows_amd64 windows_arm64

.PHONY: build test dist docker clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o pantech-mcp ./cmd/pantech-mcp

test:
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...
	go test -race ./...

dist: clean
	@mkdir -p dist/$(VERSION)
	@for p in $(PLATFORMS); do \
		os=$${p%_*}; arch=$${p#*_}; dir=$$(mktemp -d); bin=pantech-mcp; \
		if [ $$os = windows ]; then bin=pantech-mcp.exe; fi; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$dir/$$bin ./cmd/pantech-mcp || exit 1; \
		cp LICENSE README.md $$dir/; \
		if [ $$os = windows ]; then \
			(cd $$dir && zip -q -X $(CURDIR)/dist/$(VERSION)/pantech-mcp_$$p.zip $$bin LICENSE README.md) || exit 1; \
			echo "built pantech-mcp_$$p.zip"; \
		else \
			tar -czf dist/$(VERSION)/pantech-mcp_$$p.tar.gz -C $$dir $$bin LICENSE README.md || exit 1; \
			echo "built pantech-mcp_$$p.tar.gz"; \
		fi; \
		rm -rf $$dir; \
	done
	@cd dist/$(VERSION) && shasum -a 256 pantech-mcp_* > SHA256SUMS
	@echo "dist/$(VERSION): attach these to the GitHub release"

docker:
	docker build --build-arg VERSION=$(VERSION) -t pantech-mcp:$(VERSION:v%=%) .

clean:
	rm -rf dist pantech-mcp
