BINARY ?= alocal
CMD := ./cmd/alocal
GO ?= go
INSTALL_DIR ?= $(HOME)/.local/bin
DIST := dist
TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
ARGS ?=

.DEFAULT_GOAL := help
.PHONY: all build install run test race vet fmt check cross smoke clean help

all: check build

build:
	$(GO) build -trimpath -ldflags="-s -w" -o $(BINARY) $(CMD)

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "Installed $(INSTALL_DIR)/$(BINARY)"
	@echo "Add $(INSTALL_DIR) to PATH if it is not there already."

run: build
	./$(BINARY) $(ARGS)

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt'd:"; echo "$$unformatted"; exit 1; \
	fi
	$(GO) vet ./...
	$(GO) test -race ./...

cross:
	mkdir -p $(DIST)
	@for target in $(TARGETS); do \
		goos=$${target%/*}; goarch=$${target#*/}; \
		out="$(DIST)/$(BINARY)-$$goos-$$goarch"; \
		if [ "$$goos" = "windows" ]; then out="$$out.exe"; fi; \
		echo "building $$target -> $$out"; \
		GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o "$$out" $(CMD) || exit 1; \
	done

smoke: build
	./$(BINARY) catalog validate
	./$(BINARY) score --mock-hardware tests/fixtures/rtx3060.json
	./$(BINARY) recommend --mock-hardware tests/fixtures/rtx3060.json --top 1
	./$(BINARY) models --task coding --quant Q4_K_M
	./$(BINARY) models --local
	./$(BINARY) pull qwen2.5-7b-instruct-q4_k_m --file Qwen2.5-7B-Instruct-Q4_K_M.gguf --dry-run

clean:
	rm -f $(BINARY) $(BINARY).exe
	rm -rf $(DIST)

help:
	@printf '%s\n' \
	  'Alocal - build and test shortcuts' \
	  '' \
	  '  make build     build ./alocal' \
	  '  make install   install to $(INSTALL_DIR)' \
	  '  make run ARGS="scan"   build and run one command' \
	  '  make test      go test ./...' \
	  '  make race      go test -race ./...' \
	  '  make check     gofmt + vet + race tests' \
	  '  make cross     build 6 static binaries into dist/' \
	  '  make smoke     run offline smoke tests with fixtures' \
	  '  make clean     remove local binaries and dist/' \
	  ''
