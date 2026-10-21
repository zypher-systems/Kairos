# Everything builds inside Docker; nothing compiles on the host.
# TARGETS
#   build      build production images (server)
#   up/down    run the full stack
#   dev        hot-reload server + postgres (docker compose up dev)
#   test       go test -race inside the builder container
#   vet        go vet inside the builder container
#   fmt-check  gofmt check inside the builder container
#   tidy       go mod tidy inside the builder container (writes go.mod/go.sum)
#   ps/logs    stack introspection

.PHONY: build test vet fmt-check tidy up down dev ps logs web-dev web-build smoke link-check

export KAIROS_HTTP_PORT ?= 8080

build:
	docker compose build server

up:
	docker compose up -d --build

down:
	docker compose down

dev:
	docker compose up dev

ps:
	docker compose ps

logs:
	docker compose logs -f

BUILDER_EXEC = docker compose run --rm builder

test:
	docker compose exec -T postgres psql -U kairos -d postgres -c "SELECT 1 FROM pg_database WHERE datname='kairos_test'" >/dev/null 2>&1 || \
	  docker compose exec -T postgres psql -U kairos -d postgres -c "CREATE DATABASE kairos_test"
	$(BUILDER_EXEC) go test ./... -race -count=1

vet:
	$(BUILDER_EXEC) go vet ./...

fmt-check:
	$(BUILDER_EXEC) sh -c 'test -z "$$(gofmt -l cmd internal webdist migrations tools)" || { gofmt -l cmd internal webdist migrations tools; exit 1; }'

tidy:
	$(BUILDER_EXEC) go mod tidy

web-dev:
	docker compose up web-dev

web-build:
	docker compose run --rm webbuild sh -c "npx tsc --noEmit && npm run build"

smoke:
	bash scripts/smoke.sh

link-check:
	bash scripts/link-check.sh
