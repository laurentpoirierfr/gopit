# Makefile de gopit — « make help » pour la liste des cibles.
#
# Variables ajustables en ligne de commande :
#   make run ARGS=~/Projects/qrcode-gen
#   make build VERSION=1.0.0       (par défaut : git describe)

BINARY  := gopit
PKG     := ./cmd/gopit
BIN_DIR := bin
PREFIX  ?= $(HOME)/.local

# version : dernier tag git (sans le « v »), suffixée si l'arbre a divergé
# (ex. 0.3.0-2-gabc1234-dirty) ; « dev » hors d'un dépôt git ou sans commit.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS := -s -w -X github.com/laurentpoirierfr/gopit/internal/version.Version=$(or $(VERSION),dev)

# `docker` est-il l'émulation podman ? (utile à « make act »)
DOCKER ?= docker
PODMAN := $(shell $(DOCKER) version 2>&1 | grep -qi podman && echo 1)

.DEFAULT_GOAL := help

.PHONY: help build run install uninstall test cover vet fmt fmt-check check tidy clean release-snapshot act

help: ## Affiche la liste des cibles
	@sed -n -E 's/^([a-zA-Z0-9_-]+):[^#]*## ?(.*)$$/  \1  \2/p' $(MAKEFILE_LIST)

build: ## Compile le binaire dans bin/
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

run: ## Lance gopit (ARGS=répertoire d'un module Go)
	go run -ldflags "$(LDFLAGS)" $(PKG) $(ARGS)

install: build ## Installe le binaire dans $(PREFIX)/bin
	install -Dm755 $(BIN_DIR)/$(BINARY) $(PREFIX)/bin/$(BINARY)

uninstall: ## Supprime le binaire installé
	rm -f $(PREFIX)/bin/$(BINARY)

test: ## Lance les tests
	go test ./...

cover: ## Tests avec couverture
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

vet: ## Analyse statique
	go vet ./...

fmt: ## Formate le code
	gofmt -w .

fmt-check: ## Vérifie le formatage (échoue si un fichier n'est pas formaté)
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

check: fmt-check vet test ## Formatage + analyse + tests

tidy: ## Nettoie go.mod / go.sum
	go mod tidy

clean: ## Supprime bin/, dist/ et la couverture
	rm -rf $(BIN_DIR) dist/ coverage.out

release-snapshot: ## Essai local de GoReleaser dans dist/, sans rien publier
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean

# act parle à l'API Docker : avec podman, on utilise le socket utilisateur
# (systemctl --user start podman.socket), sauf si DOCKER_HOST est déjà défini.
ACT_SOCK := /run/user/$(shell id -u)/podman/podman.sock
ACT_ENV  := $(if $(and $(PODMAN),$(if $(DOCKER_HOST),,1)),DOCKER_HOST=unix://$(ACT_SOCK))

act: ## Teste un job de la CI en local avec act (JOB=check|goreleaser)
	$(ACT_ENV) act push -W .github/workflows/ci.yml -j $(or $(JOB),check)
