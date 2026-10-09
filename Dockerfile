# --- Build stage ---
# go.mod требует Go >= 1.26 — версия образа должна совпадать с версией в go.mod
FROM golang:1.26-alpine AS build

WORKDIR /app

# Кэшируем зависимости
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/chatter ./cmd/server

# --- Runtime stage ---
FROM alpine:3.20

RUN adduser -D -u 10001 chatter

WORKDIR /app

COPY --from=build /bin/chatter /app/chatter
COPY migrations /app/migrations
COPY frontend /app/frontend

USER chatter

ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/app/chatter"]
