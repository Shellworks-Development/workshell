# syntax=docker/dockerfile:1

# 1. Build the dashboard.
FROM node:24-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# 2. Build the server, embedding the dashboard and the SDK.
FROM golang:1.26-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/workshell ./cmd/workshell

# 3. Minimal runtime image.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 workshell \
    && mkdir /data && chown workshell /data
COPY --from=server /out/workshell /usr/local/bin/workshell
USER workshell
ENV WORKSHELL_DATA_DIR=/data WORKSHELL_ADDR=:8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["workshell"]
