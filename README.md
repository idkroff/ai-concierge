# AI Concierge

Telegram-бот, который по поручению пользователя звонит в организации (рестораны) и договаривается о брони.

## Сервисы

- `tg_bot` — Telegram-бот на Go: интерфейс, сессии, лимиты звонков, пользователи в YDB. Сборка и деплой — [tg_bot/DEPLOY.md](tg_bot/DEPLOY.md).
- `caller_service` — сервис звонков: Asterisk + Yandex Realtime API, поиск организаций, события звонка по WebSocket. Запуск — [caller_service/README.md](caller_service/README.md), архитектура событий — [caller_service/docs/EVENTS_ARCHITECTURE.md](caller_service/docs/EVENTS_ARCHITECTURE.md).
- `infra` — Terraform (Yandex Cloud: Kubernetes, YDB, Container Registry) и скрипты сборки.
- `asterisk_caller` — MVP-скрипт взаимодействия с SIP Trunk провайдера телефонии через клиент Asterisk.
- `yandex_realtime` — MVP-скрипт взаимодействия с агентской системой Yandex Realtime API через вебсокеты.

## Фича «Память» (проектный семинар, 4 курс)

Документация по фиче «память о пользователе и бронях» лежит в [docs/memory](docs/memory/README.md):

- требования: one-pager, PRD, SRS, User Stories, RTM — [docs/memory/requirements](docs/memory/requirements/);
- исследование подходов и технологий — [docs/memory/research.md](docs/memory/research.md);
- технический дизайн — [docs/memory/design.md](docs/memory/design.md).
