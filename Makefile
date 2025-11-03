# Prometheus to MySQL ETL - Makefile

# Project Configuration
PROJECT_NAME := prom-etl-db
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GO_VERSION := $(shell go version | awk '{print $$3}')

# Build Configuration
BINARY_NAME := prom-etl-db
MAIN_PATH := ./cmd/server
BUILD_DIR := ./build
DOCKER_REGISTRY := release.daocloud.io/ndx-product
DOCKER_IMAGE := $(DOCKER_REGISTRY)/prom-etl-db
DOCKER_TAG ?= $(shell echo $${DOCKER_TAG:-v0.1.2})

# Go Build Flags
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.goVersion=$(GO_VERSION)"

# Color Output
BLUE := \033[0;34m
GREEN := \033[0;32m
RED := \033[0;31m
NC := \033[0m

.PHONY: help
help: ## Show help information
	@echo "$(BLUE)$(PROJECT_NAME) - Development Tools$(NC)"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  $(GREEN)%-15s$(NC) %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Development Environment
.PHONY: setup
setup: ## Setup development environment
	@go mod download && go mod tidy
	@if [ ! -f .env ]; then cp env.example .env; echo "$(GREEN)Created .env file$(NC)"; fi
	@mkdir -p $(BUILD_DIR) logs

.PHONY: fmt
fmt: ## Format Go code
	@echo "$(BLUE)Formatting Go code...$(NC)"
	@go fmt ./...
	@echo "$(GREEN)Code formatting completed$(NC)"

.PHONY: clean
clean: ## Clean build files
	@rm -rf $(BUILD_DIR) logs/*
	@go clean -cache

# Build and Run
.PHONY: build
build: ## Build binary file
	@mkdir -p $(BUILD_DIR)
	@go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "$(GREEN)Build completed: $(BUILD_DIR)/$(BINARY_NAME)$(NC)"

.PHONY: debug
debug: ## Run application in debug mode
	@if [ ! -f .env ]; then echo "$(RED)Error: .env file does not exist, please run make setup first$(NC)"; exit 1; fi
	@export $$(cat .env | grep -v '^#' | xargs) && go run $(MAIN_PATH)

# Docker
.PHONY: docker-build
docker-build: ## Build Docker image (Linux x86_64)
	@docker build --platform linux/amd64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		--build-arg GO_VERSION=$(GO_VERSION) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	@echo "$(GREEN)Docker image: $(DOCKER_IMAGE):$(DOCKER_TAG)$(NC)"

.PHONY: docker-push
docker-push: ## Push Docker image
	@docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

.PHONY: docker-build-push
docker-build-push: docker-build docker-push ## Build and push Docker image

# Default Target
.DEFAULT_GOAL := help