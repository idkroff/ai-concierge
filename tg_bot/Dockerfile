FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tg_bot ./cmd/

FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata
ENV TZ=Europe/Moscow

WORKDIR /app

COPY --from=builder /app/tg_bot .

CMD ["./tg_bot"]
