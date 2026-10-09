# --- Build stage ---
FROM golang:1.22-alpine AS build

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
