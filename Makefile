VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := linux-amd64 linux-arm64 darwin-arm64 darwin-amd64
PUBLISH := $(or $(XDG_DATA_HOME),$(HOME)/.local/share)/agent-relay/dist

.PHONY: build test install dist publish

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o arl ./cmd/arl

test:
	go vet ./... && go test ./...

install: build
	install -m755 arl $(HOME)/.local/bin/arl

dist:
	@mkdir -p dist
	@for t in $(TARGETS); do \
		GOOS=$${t%-*} GOARCH=$${t#*-} go build -trimpath -ldflags "$(LDFLAGS)" -o dist/arl-$$t ./cmd/arl || exit 1; \
	done

# On the hub machine: install locally and let the other machines 'arl update'.
publish: test dist install
	@mkdir -p $(PUBLISH)
	cp dist/arl-* $(PUBLISH)/
	@echo "published $(VERSION) to $(PUBLISH)"
