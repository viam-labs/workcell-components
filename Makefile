GO_BUILD_ENV :=
GO_BUILD_FLAGS :=
MODULE_BINARY := bin/workcell-components
VERSION := $(shell cat VERSION 2>/dev/null)
APP_HTML := apps/workcell/index.html
PLATFORM ?= linux/amd64

$(MODULE_BINARY): Makefile go.mod *.go cmd/module/*.go
	$(GO_BUILD_ENV) go build $(GO_BUILD_FLAGS) -o $(MODULE_BINARY) cmd/module/main.go

lint:
	gofmt -s -w .

update:
	go get go.viam.com/rdk@latest
	go mod tidy

test:
	go test ./...

# stamp-version rewrites the version badge in the workcell-tuner app's
# HTML from the VERSION file. The <!--V-->...<!--/V--> markers keep the
# substitution idempotent.
stamp-version:
	@if [ -z "$(VERSION)" ]; then echo "VERSION file missing or empty"; exit 1; fi
	@sed -i.bak 's|<!--V-->[^<]*<!--/V-->|<!--V-->$(VERSION)<!--/V-->|' $(APP_HTML)
	@rm -f $(APP_HTML).bak
	@echo "Stamped $(APP_HTML) with v$(VERSION)"

module.tar.gz: meta.json $(MODULE_BINARY) $(APP_HTML) VERSION
	$(MAKE) stamp-version
	strip $(MODULE_BINARY)
	tar czf $@ meta.json $(MODULE_BINARY) apps

module: test module.tar.gz

all: test module.tar.gz

# publish builds the tarball and uploads it using the version from VERSION.
# Override PLATFORM=linux/arm64 (etc) for other architectures.
publish: module.tar.gz
	viam module upload --version $(VERSION) --platform $(PLATFORM) module.tar.gz

setup:
	go mod tidy
