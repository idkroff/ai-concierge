# Сборка и деплой tg-bot

## Пререквизиты

- Docker с buildx
- `yc` CLI (авторизован)
- `kubectl` (настроен на кластер)
- Terraform applied (`infra/terraform/`)

## 1. Настройка kubectl

```bash
# одноразово
bash infra/init_kubectl.sh
```

## 2. Сборка и пуш образа

```bash
# из корня репозитория
bash infra/build_containers.sh --tg_bot_version=1.1.0
```

Скрипт берёт `registry_id` из `terraform output`, собирает образ под `linux/amd64` и пушит в `cr.yandex`.

## 3. Деплой

Обновить версию в `infra/terraform/terraform.tfvars`:

```hcl
tg_bot_version = "1.1.0"
```

Применить:

```bash
cd infra/terraform
terraform apply
```

Или без Terraform — напрямую через kubectl:

```bash
kubectl -n concierge set image deployment/tg-bot \
  tg-bot=cr.yandex/<REGISTRY_ID>/tg-bot:1.1.0

kubectl -n concierge rollout status deployment/tg-bot
```

## 4. Проверка

```bash
kubectl -n concierge get pods -l app=tg-bot
kubectl -n concierge logs -f deployment/tg-bot
```

## Env-переменные

Задаются в `infra/terraform/kubernetes.tf` (deployment `tg-bot`):

| Переменная | Описание |
|------------|----------| 
| `BOT_TOKEN` | Telegram bot token |
| `CALLER_SERVICE_URL` | WebSocket URL caller-сервиса (`ws://voice-agent-service:8080`) |
| `YDB_DSN` | Строка подключения к YDB (нужно добавить в deployment) |

> **TODO:** добавить `YDB_DSN` в env deployment'а в `kubernetes.tf`.
