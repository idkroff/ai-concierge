.PHONY: build run tidy

tidy:
	go mod tidy

build: tidy
	go build -o bin/tg_bot ./cmd/

run:
	go run ./cmd/
