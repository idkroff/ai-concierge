# Dockerfile для voice_agent приложения
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Копируем go mod файлы
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходный код
COPY . .

# Собираем бинарник для linux/amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o voice_agent ./cmd/voice_agent

# Финальный образ
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata
ENV TZ=Europe/Moscow

WORKDIR /app

# Копируем бинарник из builder
COPY --from=builder /app/voice_agent .

# Открываем порт для HTTP API
EXPOSE 8080

# Запуск приложения
CMD ["./voice_agent"]

