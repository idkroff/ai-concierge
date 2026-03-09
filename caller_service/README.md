## Что нужно

1. **Docker** и **Docker Compose**
2. **Go 1.21+**
3. **Yandex Cloud** аккаунт (API ключ и Folder ID)
4. **SIP trunk** (например, zvonok.com, plusofon.ru)

## Быстрый старт

### 1. Настройка креденшалов

Создайте файл `.env`

Заполните его своими данными:

```bash
# SIP credentials для plusofon.ru
SIP_USER=ваш_sip_логин
SIP_PASS=ваш_sip_пароль

# Yandex Cloud
API_KEY=ваш_api_key
FOLDER=ваш_folder_id

# HTTP Server (optional, default: 8080)
HTTP_PORT=8080
```

### 2. Генерация конфигурации SIP

Конфигурация Asterisk генерируется автоматически из шаблона `config/asterisk/pjsip.conf.template`:

```bash
make config
```

Эта команда создаст файл `config/asterisk/pjsip.conf` с вашими SIP креденшалами. Файл не будет коммититься в git (.gitignore).

### 3. Запуск Asterisk

```bash
make docker-up
```

Или вручную:

```bash
cd config
docker-compose up -d
```

> **Примечание:** `make docker-up` автоматически вызовет `make config` для генерации pjsip.conf

**Если изменили конфигурацию** (`pjsip.conf`, `extensions.conf` и т.д.), пересоберите образ:

```bash
cd config
docker-compose down
docker-compose build --no-cache
docker-compose up -d
```

Проверьте что SIP зарегистрирован:

```bash
docker exec asterisk-caller asterisk -rx "pjsip show registrations"
```

Должно показать статус **Registered**.

### 4. Запуск сервиса

```bash
make serve
```

Или вручную:

```bash
go run cmd/voice_agent/main.go
```

Сервер запустится на `http://localhost:8080` (или на порту из `HTTP_PORT`).

### 5. Совершение звонка

Через API:

```bash
curl "http://localhost:8080/call/start?phone_number=79914043003"
```

Ответ:
```json
{
  "status": "ok",
  "call_id": "550e8400-e29b-41d4-a716-446655440000"
}
```
