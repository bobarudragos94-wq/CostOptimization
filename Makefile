GO      ?= go
VERSION := $(shell grep 'AgentVersion =' internal/model/model.go | cut -d'"' -f2)
LDFLAGS := -s -w
DIST    := dist

.PHONY: all build build-windows test vet e2e demo sbom clean

all: build build-windows

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/ura-agent ./cmd/ura-agent
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/ura-analyzer ./cmd/ura-analyzer

build-windows:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/ura-agent.exe ./cmd/ura-agent
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/ura-analyzer.exe ./cmd/ura-analyzer

test:
	$(GO) vet ./...
	$(GO) test ./...

e2e:
	$(GO) test ./test/e2e/ -v

# Full offline demo: keygen -> synthetic fleet -> encrypted bundles -> reports.
demo: build
	./scripts/demo.sh

# Dependency lock check + SBOM (SPDX-like module list; see scripts/sbom.sh).
sbom:
	./scripts/sbom.sh

# Release layout: platform zips with deploy scripts and docs.
dist: all sbom
	rm -rf $(DIST) && mkdir -p $(DIST)/linux $(DIST)/windows $(DIST)/analyzer
	cp bin/ura-agent deploy/linux/* configs/agent.example.yaml $(DIST)/linux/
	cp bin/ura-agent.exe deploy/windows/* configs/agent.example.yaml $(DIST)/windows/
	cp bin/ura-analyzer bin/ura-analyzer.exe $(DIST)/analyzer/
	cp -r docs deploy/sql $(DIST)/
	cp sbom.json $(DIST)/
	cd $(DIST) && tar czf ura-$(VERSION)-linux-amd64.tar.gz linux docs sql sbom.json \
		&& zip -qr ura-$(VERSION)-windows-amd64.zip windows docs sql sbom.json \
		&& tar czf ura-$(VERSION)-analyzer.tar.gz analyzer docs sbom.json
	@echo "release artifacts in $(DIST)/"

clean:
	rm -rf bin $(DIST) demo-out sbom.json
