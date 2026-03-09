.PHONY: help build serve test-call clean docker-up docker-down docker-logs test lint config

# Параметры
BINARY_NAME=voice_agent
PHONE?=79914043003
PORT?=8080

help: ## Показать помощь
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

config: ## Сгенерировать pjsip.conf из шаблона
	@echo "⚙️  Генерация конфигурации из шаблона..."
	@if [ ! -f .env ]; then \
		echo "❌ Файл .env не найден. Скопируйте env.example в .env и заполните данные:"; \
		echo "   cp env.example .env"; \
		exit 1; \
	fi
	@export $$(cat .env | grep -v '^#' | xargs) && \
		envsubst < config/asterisk/pjsip.conf.template > config/asterisk/pjsip.conf
	@echo "✅ Конфигурация сгенерирована: config/asterisk/pjsip.conf"

build: ## Собрать бинарник
	@echo "🔨 Сборка..."
	go build -o $(BINARY_NAME) cmd/voice_agent/main.go
	@echo "✅ Готово: ./$(BINARY_NAME)"

serve: ## Запустить HTTP сервис
	@echo "🚀 Запуск Voice Agent Service..."
	go run cmd/voice_agent/main.go

test-call: ## Тестовый звонок через API (make test-call PHONE=79991234567)
	@echo "📞 Звонок на $(PHONE) через API..."
	@curl -s "http://localhost:$(PORT)/call/start?phone_number=$(PHONE)" | jq .

docker-up: config ## Запустить Asterisk в Docker
	@echo "🐳 Запуск Asterisk..."
	cd config && docker-compose up -d
	@echo "✅ Asterisk запущен"

docker-down: ## Остановить Asterisk
	@echo "🛑 Остановка Asterisk..."
	cd config && docker-compose down
	@echo "✅ Asterisk остановлен"

docker-logs: ## Показать логи Asterisk
	docker logs -f asterisk-caller

docker-status: ## Проверить статус Asterisk
	@echo "📊 Статус регистрации:"
	@docker exec asterisk-caller asterisk -rx "pjsip show registrations" || echo "❌ Asterisk не запущен"
	@echo "\n📊 Активные каналы:"
	@docker exec asterisk-caller asterisk -rx "core show channels" || echo "❌ Asterisk не запущен"

docker-console: ## Консоль Asterisk
	docker exec -it asterisk-caller asterisk -rvvv

docker-check-audiosocket: ## Проверить модуль AudioSocket в Asterisk
	@echo "🔍 Проверка модуля AudioSocket..."
	@docker exec asterisk-caller asterisk -rx "module show like audiosocket" || echo "⚠️  Модуль не загружен"
	@echo "\n📦 Проверка наличия файла модуля..."
	@docker exec asterisk-caller ls -la /usr/lib/asterisk/modules/ | grep audiosocket || echo "⚠️  Файл модуля не найден"

clean: ## Удалить временные файлы
	@echo "🧹 Очистка..."
	rm -f $(BINARY_NAME)
	rm -f sounds/*.wav
	rm -f recordings/*.wav
	@echo "✅ Очищено"

deps: ## Установить зависимости
	@echo "📦 Установка зависимостей..."
	go mod download
	@echo "✅ Зависимости установлены"

test: ## Запустить тесты
	@echo "🧪 Запуск тестов..."
	go test -v ./...

lint: ## Проверить код линтером
	@echo "🔍 Проверка кода..."
	golangci-lint run ./...

fmt: ## Форматировать код
	@echo "✨ Форматирование..."
	go fmt ./...
	@echo "✅ Код отформатирован"

install: ## Установить бинарник в $GOPATH/bin
	@echo "📦 Установка..."
	go install cmd/voice_agent/main.go
	@echo "✅ Установлено в $GOPATH/bin"

test-parallel: ## Тестовые параллельные звонки
	@echo "📞 Параллельные звонки через API..."
	@curl -s "http://localhost:$(PORT)/call/start?phone_number=79914043003" & \
	curl -s "http://localhost:$(PORT)/call/start?phone_number=79991234567" & \
	wait
	@echo "\n✅ Запросы отправлены, проверьте логи сервера"

dev: docker-up serve ## Запустить в режиме разработки

stop: docker-down clean ## Остановить все и очистить

.DEFAULT_GOAL := help
