# syntax=docker/dockerfile:1.7
# Dockerfile multi-stage compartilhado pelos serviços Go.
# Uso: docker build -f infra/docker/Dockerfile.go --build-arg CMD_PATH=./ingestor-go/cmd/ingestor .

FROM golang:1.25-alpine AS build
ARG CMD_PATH
WORKDIR /src
ENV CGO_ENABLED=0

# Copia o código e resolve dependências (go.sum é gerado/atualizado aqui se ainda não existir).
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod tidy && \
    go build -trimpath -ldflags="-s -w" -o /out/app ${CMD_PATH}

# Imagem final mínima, sem shell, executando como usuário não-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
