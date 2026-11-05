GO ?= go
NPM_FLAGS ?=
.PHONY: web build linux windows test run
web:
	cd web && npm ci $(NPM_FLAGS) && npm run build
	rm -rf internal/webui/dist
	mkdir -p internal/webui/dist
	cp -a web/dist/. internal/webui/dist/
build:
	mkdir -p dist
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '-s -w' -o dist/ProjNote ./cmd/projnote
linux: build
windows:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags '-s -w' -o dist/ProjNote.exe ./cmd/projnote
test:
	$(GO) test ./...
	$(GO) vet ./...
run:
	$(GO) run ./cmd/projnote --data-dir ./data
