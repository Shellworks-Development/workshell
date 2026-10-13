.PHONY: build web server dev dev-web test sdk sim clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

## build: build the dashboard and a single self-contained server binary at bin/workshell
build: web server

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

server:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/workshell ./cmd/workshell

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

clean:
	rm -rf bin web/dist/assets web/dist/index.html web/dist/favicon.svg
