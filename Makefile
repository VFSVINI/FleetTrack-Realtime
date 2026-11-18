.DEFAULT_GOAL := help
COMPOSE ?= docker compose

.PHONY: help env up down restart logs ps build tidy fmt vet lint test sim sim-batch clean

help: ## Lista os comandos disponíveis
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

env: ## Cria o .env a partir do .env.example (se não existir)
	@test -f .env || cp .env.example .env

up: env ## Sobe toda a stack (build + background)
	$(COMPOSE) up --build -d
	@echo "Dashboard:  http://localhost:3000"
	@echo "Swagger:    http://localhost:8081/docs"
	@echo "RabbitMQ:   http://localhost:15672  (guest/guest)"

down: ## Derruba a stack (mantém volumes)
	$(COMPOSE) down

restart: ## Reinicia os serviços Go
	$(COMPOSE) restart ingestor processor api-gateway

logs: ## Acompanha os logs dos serviços Go
	$(COMPOSE) logs -f ingestor processor api-gateway

ps: ## Status dos containers
	$(COMPOSE) ps

build: ## Build das imagens
	$(COMPOSE) --profile sim build

tidy: ## Gera/atualiza go.sum (rode uma vez e faça commit)
	go mod tidy

fmt: ## Formata o código
	gofmt -w .

vet: ## go vet
	go vet ./...

lint: ## golangci-lint
	golangci-lint run ./...

test: ## Testes unitários com race detector
	go test -race -count=1 ./...

sim: ## Simulador: 30 veículos, 1 requisição por veículo/s
	$(COMPOSE) --profile sim up -d simulator

sim-batch: ## Simulador local em modo lote (requer Go instalado)
	go run ./simulator/cmd/simulator -url http://localhost:8080 -vehicles 200 -batch

clean: ## Remove containers E volumes (apaga os dados)
	$(COMPOSE) --profile sim down -v
