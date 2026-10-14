.PHONY: build web server dist dev dev-web test sdk sim sim-live clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

## build: build the dashboard and a single self-contained server binary at bin/workshell
build: web server

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

server:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/workshell ./cmd/workshell

PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

## dist: release archives for every platform in dist/ (run `make web` first), plus SHA256SUMS
dist:
	rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		name=workshell_$(VERSION)_$${os}_$${arch}; \
		echo "building $$name"; \
		mkdir -p dist/$$name && cp LICENSE README.md dist/$$name/ && \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$$name/workshell$$ext ./cmd/workshell || exit 1; \
		if [ $$os = windows ]; then (cd dist && zip -qr $$name.zip $$name); else tar -C dist -czf dist/$$name.tar.gz $$name; fi; \
		rm -rf dist/$$name; \
	done
	cd dist && sha256sum workshell_* > SHA256SUMS

## dev: run the server on :8080 (all interfaces, so it's reachable on your LAN)
dev:
	go run ./cmd/workshell -debug

## dev-web: dashboard with hot reload on :5173, also on your LAN (open http://<your-ip>:5173 on a phone)
dev-web:
	cd web && pnpm dev

## test: Go tests, dashboard type check, and the SDK test against Roblox mocks
test:
	go vet ./...
	go test ./...
	cd web && pnpm typecheck
	python3 sdk/test/run.py

## sdk: build the SDK as a Roblox model with Rojo
sdk:
	rojo build sdk/default.project.json -o bin/Workshell.rbxm

## sim: send a week of fake telemetry (KEY=wsk_... [URL=http://localhost:8080])
sim:
	go run ./cmd/workshell-sim -url $(or $(URL),http://localhost:8080) -key $(KEY)

## sim-live: stream live traffic from mock game servers until Ctrl-C (KEY=wsk_... [URL=...] [SERVERS=5])
sim-live:
	go run ./cmd/workshell-sim -url $(or $(URL),http://localhost:8080) -key $(KEY) -days 0.03 -servers $(or $(SERVERS),5) -realtime

clean:
	rm -rf bin web/dist/assets web/dist/index.html web/dist/favicon.svg
