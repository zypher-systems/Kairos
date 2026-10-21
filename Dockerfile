FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
ARG VERSION=1.18.0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /webdist/dist ./webdist/dist
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w -X github.com/zypher-systems/kairos/internal/version.Version=${VERSION}" -o /out/kairosd ./cmd/kairosd
# Agent binaries for the dashboard push-install and direct download.
# Cross-compiled from the same tree — kairos-agent is CGO-free.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="-s -w -X github.com/zypher-systems/kairos/internal/version.Version=${VERSION}" \
      -o /out/agent/kairos-agent-linux-amd64 ./cmd/kairos-agent \
 && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags="-s -w -X github.com/zypher-systems/kairos/internal/version.Version=${VERSION}" \
      -o /out/agent/kairos-agent-linux-arm64 ./cmd/kairos-agent

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata curl git && adduser -D -u 10001 kairos
COPY --from=build /out/kairosd /usr/local/bin/kairosd
COPY --from=build /out/agent /usr/local/share/kairos/agent
RUN mkdir -p /var/lib/kairos && chown kairos:kairos /var/lib/kairos
USER kairos
EXPOSE 8080
ENTRYPOINT ["kairosd"]