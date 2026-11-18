# FleetTrack-Realtime

Plataforma de **rastreamento de frotas e telemetria IoT em tempo real**, escrita em Go. Dispositivos enviam posição, velocidade, combustível e estado do motor; o sistema valida, enfileira, aplica regras de negócio (excesso de velocidade, combustível crítico, geofencing), mantém o estado atual em cache e guarda o histórico em um banco de séries temporais.

![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go) ![License](https://img.shields.io/badge/license-MIT-green)

## Sumário

- [Arquitetura](#arquitetura)
- [Decisões técnicas](#decisões-técnicas)
- [Pré-requisitos](#pré-requisitos)
- [Guia rápido](#guia-rápido)
- [Serviços e portas](#serviços-e-portas)
- [API de ingestão](#api-de-ingestão-ingestor)
- [API de consulta](#api-de-consulta-api-gateway)
- [Regras de negócio](#regras-de-negócio)
- [Teste de carga](#teste-de-carga-com-o-simulador)
- [Configuração](#configuração)
- [Desenvolvimento e testes](#desenvolvimento-e-testes)
- [Limitações conhecidas](#limitações-conhecidas-e-próximos-passos)

## Arquitetura

```mermaid
flowchart LR
    SIM[simulator<br/>veículos IoT] -- "HTTP JSON" --> ING

    subgraph Ingestão
        ING[ingestor-go<br/>Fiber]
    end

    ING -- "publica<br/>(pool de canais AMQP)" --> RMQ[(RabbitMQ<br/>telemetry.raw)]
    RMQ -- "nack sem requeue" --> DLQ[(telemetry.dead)]

    subgraph Processamento
        PROC[processor-go<br/>worker pool]
        BW[batch writer<br/>COPY]
    end

    RMQ -- "consume<br/>prefetch" --> PROC
    PROC -- "estado atual,<br/>cooldown, cercas" --> REDIS[(Redis)]
    PROC --> BW -- "lote" --> TSDB[(PostgreSQL +<br/>TimescaleDB)]
    PROC -- "alertas" --> TSDB

    subgraph Consulta
        API[api-gateway<br/>Gin + OpenAPI]
        NGX[frontend<br/>nginx]
    end

    REDIS -- "posição atual" --> API
    TSDB -- "histórico, métricas,<br/>alertas" --> API
    API --> NGX --> UI([Dashboard<br/>Leaflet + Chart.js])
```

Fluxo de uma leitura de telemetria:

1. O dispositivo faz `POST /v1/telemetry` no **ingestor**. O pacote é validado e entregue a um pool de publicadores; a API responde `202 Accepted` sem esperar o banco.
2. O **processor** consome a fila com `prefetch`, e cada worker: atualiza a posição no Redis, avalia as regras (velocidade, combustível, cercas), grava alertas e entrega a telemetria ao *batch writer*.
3. O *batch writer* grava em lote (por tamanho ou por tempo) usando o protocolo `COPY` do PostgreSQL em uma *hypertable* do TimescaleDB.
4. O **api-gateway** lê a posição atual no Redis (1 round-trip) e o histórico/métricas/alertas no TimescaleDB. O **frontend** consulta a API a cada 2–3 s.

### Estrutura do repositório

```
.
├── ingestor-go/        # cmd/ingestor + internal/{handler,publisher}
├── processor-go/       # cmd/processor + internal/{rules,service,cache,storage,worker}
├── api-gateway/        # cmd/api + internal/{handler,repository,docs}
├── simulator/          # cmd/simulator (gerador de carga)
├── frontend/           # dashboard estático servido pelo nginx
├── pkg/                # código compartilhado: config, logger, models, mq, db
├── infra/              # sql/ (TimescaleDB), rabbitmq/, docker/ (Dockerfile multi-stage)
├── .github/workflows/  # CI (lint + testes)
├── docker-compose.yml
└── Makefile
```

Um único `go.mod` na raiz (módulo `fleettrack`) simplifica o compartilhamento do pacote `pkg/` e o cache de build do Docker.

## Decisões técnicas

| Decisão | Motivo |
|---|---|
| **Fiber na ingestão, Gin na consulta** | Fiber (fasthttp) reduz alocações no caminho quente de escrita; Gin é mais ergonômico e usa `net/http` padrão, o que facilita middlewares, testes e shutdown gracioso na API de leitura. |
| **RabbitMQ entre ingestão e processamento** | Desacopla picos de entrada da velocidade do banco. A fila é durável, as mensagens são persistentes e mensagens inválidas vão para uma *dead-letter queue* em vez de travar o consumo. |
| **Pool de canais AMQP no ingestor** | Canais AMQP não são thread-safe; cada worker tem o seu e consome de um `chan []byte` com buffer. Buffer cheio → `503` + `Retry-After` (backpressure explícito). |
| **Worker pool + `prefetch` no processor** | `prefetch` limita mensagens em voo; N goroutines leem do mesmo canal de entregas. Mensagens inválidas → DLQ; falhas transitórias → requeue. |
| **Batch insert com `COPY`** | É a forma mais rápida de ingerir no Postgres. O lote fecha por tamanho (`BATCH_SIZE`) ou tempo (`BATCH_INTERVAL`), com retentativas e backoff exponencial. |
| **TimescaleDB** | Hypertable com chunks de 1 dia, índice `(vehicle_id, time DESC)`, compressão após 7 dias, retenção de 90 dias e *continuous aggregate* de 5 min (`telemetry_5m`) para estatísticas por veículo. |
| **Redis para estado atual** | Um hash (`fleet:positions`) com a última posição de cada veículo: o mapa lê a frota inteira com um único `HGETALL`. Também guarda o estado das cercas e o *cooldown* de alertas (`SET NX EX`). |
| **Cooldown de alertas** | Evita tempestade de alertas repetidos (um veículo em excesso de velocidade geraria um alerta por segundo) e *flapping* de GPS na borda das cercas. |
| **Regras puras em `rules/`** | Sem I/O, 100% testáveis em tabela. O serviço injeta o Redis/Postgres por interfaces e é testado com fakes. |
| **Graceful shutdown** | `signal.NotifyContext` em todos os serviços: o ingestor para de aceitar requisições e drena o buffer; o processor cancela o consumer, termina as mensagens em voo e faz *flush* final do lote. |
| **Logs JSON com `slog`** | Campo `service` em todas as linhas; nível via `LOG_LEVEL`. |

## Pré-requisitos

- Docker 24+ com Docker Compose v2
- `make` (opcional, mas recomendado)
- Go 1.22+ (apenas para rodar testes, lint ou o simulador fora do Docker)

## Guia rápido

```bash
# 1. Sobe toda a stack (cria o .env a partir do .env.example)
make up                    # ou: docker compose up --build -d

# 2. Gera tráfego: 30 veículos circulando pelo Recife
make sim                   # ou: docker compose --profile sim up -d simulator

# 3. Abra o dashboard
open http://localhost:3000
```

> **Primeira vez:** o repositório não traz `go.sum`. O build do Docker executa `go mod tidy` automaticamente, mas rode `make tidy` uma vez localmente e faça o commit do `go.sum` para builds reproduzíveis e cache no CI.

Outros comandos úteis: `make logs`, `make ps`, `make down`, `make clean` (apaga volumes), `make help`.

## Serviços e portas

| Serviço | URL | Observação |
|---|---|---|
| Dashboard | http://localhost:3000 | nginx; faz proxy de `/api/*` para o gateway |
| API Gateway | http://localhost:8081 | Swagger UI em `/docs`, spec em `/openapi.yaml` |
| Ingestor | http://localhost:8080 | `POST /v1/telemetry` |
| RabbitMQ (UI) | http://localhost:15672 | `guest` / `guest` |
| PostgreSQL | localhost:5432 | `fleet` / `fleet` / banco `fleet` |
| Redis | localhost:6379 | |

## API de ingestão (ingestor)

### `POST /v1/telemetry`

```bash
curl -i -X POST http://localhost:8080/v1/telemetry \
  -H 'Content-Type: application/json' \
  -d '{
    "vehicle_id": "VEH-001",
    "latitude": -8.0631,
    "longitude": -34.8711,
    "speed_kmh": 54.2,
    "fuel_level": 63.5,
    "engine_status": "on",
    "timestamp": "2026-11-18T14:30:00Z"
  }'
```

| Status | Significado |
|---|---|
| `202` | Aceito para processamento assíncrono |
| `400` | JSON malformado |
| `422` | Falha de validação (campos fora do intervalo, `timestamp` no futuro ou > 24h, `engine_status` ≠ `on`/`off`/`idle`) |
| `503` | Buffer cheio; reenvie após o `Retry-After` |

### `POST /v1/telemetry/batch`

Corpo: array de até `MAX_BATCH_SIZE` (padrão 500) pacotes. Retorna `202` com `{"accepted": n, "rejected": m, "errors": [{"index": i, "error": "..."}]}`; itens inválidos são rejeitados individualmente.

### `GET /health`

Contadores de aceitos, rejeitados, publicados e falhas de publicação.

## API de consulta (api-gateway)

Documentação interativa: **http://localhost:8081/docs**. Listas retornam `{"data": [...], "count": n}`.

| Método | Rota | Descrição |
|---|---|---|
| GET | `/api/v1/fleet` | Posição atual de toda a frota (Redis) |
| GET | `/api/v1/vehicles/{id}` | Posição atual de um veículo |
| GET | `/api/v1/vehicles/{id}/history?from=&to=&limit=` | Histórico de rota (RFC 3339; padrão: última hora; `limit` ≤ 10000) |
| GET | `/api/v1/metrics/summary` | Veículos online/em movimento, velocidade média, alertas ativos, mensagens/min |
| GET | `/api/v1/metrics/speed-series?minutes=30` | Velocidade média e volume por minuto (`time_bucket`) |
| GET | `/api/v1/metrics/vehicles` | Estatísticas das últimas 24h por veículo (*continuous aggregate*) |
| GET | `/api/v1/alerts?status=active\|all&limit=` | Alertas |
| POST | `/api/v1/alerts/{id}/ack` | Reconhece um alerta |
| GET | `/api/v1/geofences` | Cercas virtuais ativas |
| GET | `/health` | Saúde da API, Redis e banco |

Um veículo é considerado *online* se a última leitura tem menos de `ONLINE_WINDOW` (padrão 30 s).

## Regras de negócio

| Regra | Condição | Severidade |
|---|---|---|
| `SPEEDING` | `speed_kmh > SPEED_LIMIT_KMH` (100) | `warning`; `critical` acima de `SPEED_CRITICAL_KMH` (120) |
| `LOW_FUEL` | `fuel_level <= FUEL_CRITICAL_PCT` (15) | `critical` |
| `GEOFENCE_ENTER` / `GEOFENCE_EXIT` | Transição dentro/fora de uma cerca circular (Haversine) | `info` / `warning` |

- A primeira observação de um veículo em relação a uma cerca apenas registra o estado, sem alerta.
- Cada par veículo/regra respeita o `ALERT_COOLDOWN` (padrão 1 min).
- As cercas ficam na tabela `geofences` (3 exemplos no Recife em `infra/sql/002_seed.sql`) e são recarregadas pelo processor a cada 30 s. Para adicionar uma:

```sql
INSERT INTO geofences (name, latitude, longitude, radius_m) VALUES ('Garagem Central', -8.0476, -34.8770, 500);
```

## Teste de carga com o simulador

O simulador move veículos por um passeio aleatório na Grande Recife (cerca de 20% deles aceleram acima do limite, e o combustível diminui com a distância), então os alertas aparecem naturalmente.

```bash
# Via Docker (30 veículos, 1 req/veículo/s)
SIM_VEHICLES=100 make sim

# Localmente (requer Go): 1 requisição por veículo
go run ./simulator/cmd/simulator -url http://localhost:8080 -vehicles 500 -interval 1s

# Localmente, modo lote: 1 requisição agrupando todos os veículos por tick
go run ./simulator/cmd/simulator -vehicles 5000 -interval 1s -batch -duration 2m
```

| Flag | Padrão | Descrição |
|---|---|---|
| `-url` | `http://localhost:8080` | URL base do ingestor |
| `-vehicles` | `30` | Veículos simulados |
| `-interval` | `1s` | Intervalo entre envios por veículo |
| `-timescale` | `30` | Acelera o tempo simulado para o movimento ficar visível no mapa |
| `-batch` | `false` | Envia lotes (até 500 itens por requisição) |
| `-duration` | `0` | Duração do teste (0 = até Ctrl+C) |

O simulador loga a cada 5 s o total enviado, sucesso, falhas e `msgs_per_sec`. Para acompanhar o sistema durante o teste:

- **Fila:** http://localhost:15672 → *Queues* → `telemetry.raw`. Se *Ready* cresce continuamente, o processor está mais lento que a ingestão: aumente `PROCESSOR_WORKERS` ou suba réplicas (`docker compose up -d --scale processor=3`).
- **Backpressure:** respostas `503` no simulador indicam que o buffer do ingestor encheu.
- **Throughput no banco:** `SELECT count(*) FROM telemetry WHERE time > now() - interval '1 minute';`

## Configuração

Todas as variáveis têm padrão e podem ser sobrescritas em `.env` (veja `.env.example`).

| Variável | Serviço | Padrão | Descrição |
|---|---|---|---|
| `LOG_LEVEL` | todos | `info` | `debug`, `info`, `warn`, `error` |
| `RABBITMQ_URL` | ingestor, processor | `amqp://guest:guest@localhost:5672/` | |
| `DATABASE_URL` | processor, api | `postgres://fleet:fleet@localhost:5432/fleet?sslmode=disable` | |
| `REDIS_ADDR` | processor, api | `localhost:6379` | |
| `INGESTOR_PORT` | ingestor | `8080` | |
| `PUBLISHER_WORKERS` | ingestor | `8` | Canais AMQP publicadores |
| `PUBLISHER_QUEUE_SIZE` | ingestor | `10000` | Buffer interno antes do `503` |
| `MAX_BATCH_SIZE` | ingestor | `500` | Itens por requisição em lote |
| `PROCESSOR_WORKERS` | processor | `8` | Goroutines consumidoras |
| `PREFETCH` | processor | `workers × 8` | Mensagens em voo |
| `BATCH_SIZE` / `BATCH_INTERVAL` | processor | `500` / `1s` | Critérios de flush do lote |
| `SPEED_LIMIT_KMH` / `SPEED_CRITICAL_KMH` | processor | `100` / `120` | |
| `FUEL_CRITICAL_PCT` | processor | `15` | |
| `ALERT_COOLDOWN` | processor | `1m` | |
| `GEOFENCE_REFRESH` | processor | `30s` | |
| `API_PORT` | api-gateway | `8081` | |
| `ONLINE_WINDOW` | api-gateway | `30s` | |

## Desenvolvimento e testes

```bash
make test      # go test -race ./...
make vet       # go vet
make lint      # golangci-lint
make fmt       # gofmt -w .
```

Cobertura atual dos testes: validação de telemetria, regras de negócio (velocidade, combustível, Haversine, transições de cerca), serviço do processor com fakes (fluxo de cercas e cooldown), *batch writer* (flush por tamanho, por tempo e no encerramento) e handlers HTTP do ingestor.

O workflow `.github/workflows/ci.yml` roda `go vet`, `golangci-lint`, `go test -race` e o build das imagens a cada push/PR.

## Limitações conhecidas e próximos passos

- **Semântica de entrega:** o processor confirma (`ack`) a mensagem assim que ela entra no *batch writer*, antes do flush no banco. Um crash entre os dois pode perder até um lote. Para garantia total, confirme após o flush (ack por lote) e/ou use *publisher confirms* no ingestor.
- **Reconexão:** se a conexão com o RabbitMQ cai, o serviço encerra com erro e o Docker (`restart: unless-stopped`) o reinicia. Em produção, prefira reconexão in-process.
- **Ordenação:** mensagens do mesmo veículo podem ser processadas fora de ordem entre workers; a última escrita no Redis vence. Particionar por veículo (consistent-hash exchange) resolveria.
- **Segurança:** sem autenticação nos endpoints, credenciais padrão e CORS aberto. Adicione API keys/JWT, TLS e segredos antes de expor.
- **Dashboard:** usa *polling* (2–3 s). Server-Sent Events ou WebSocket reduziriam latência e tráfego.
- **Dependências do frontend** (Tailwind, Leaflet, Chart.js, tiles do CARTO) são carregadas por CDN; para uso offline, faça o *vendoring* dos arquivos.

## Licença

[MIT](LICENSE)
