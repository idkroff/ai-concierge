# Архитектура событийного взаимодействия Voice Agent

## Общая схема

```
Клиент (браузер, websocat, скрипт)
        │
        │ WebSocket /ws
        ▼
┌─────────────────────────────────────────────────────────┐
│  WSHandler                                               │
│  - принимает команды (JSON): { "action", "phone_number" }│
│  - при start_call создаёт WSEmitter и вызывает           │
│    CallService.HandleCall(callID, phone, emitter)        │
└─────────────────────────────────────────────────────────┘
        │
        │ events.Emitter (интерфейс)
        ▼
┌─────────────────────────────────────────────────────────┐
│  CallService.HandleCall                                  │
│  - оркестрирует звонок: Yandex Realtime + Asterisk       │
│  - в ключевых точках вызывает em.Emit(events.New...( ))  │
│  - не знает, куда уходят события (WS, лог, метрики)      │
└─────────────────────────────────────────────────────────┘
        │
        │ Emit(Event)
        ▼
┌─────────────────────────────────────────────────────────┐
│  Реализации Emitter:                                     │
│  - WSEmitter   → пишет JSON в WebSocket                  │
│  - NoopEmitter → ничего не делает (HTTP /call/start)     │
│  - при необходимости: LogEmitter, MetricsEmitter и т.д.   │
└─────────────────────────────────────────────────────────┘
```

Сервис не привязан к транспорту: один и тот же `HandleCall` может эмитить в WebSocket, в лог или в систему метрик — в зависимости от переданной реализации `Emitter`.

---

## Формат события

Все события имеют единый envelope:

```json
{
  "type": "yandex.text_delta",
  "call_id": "550e8400-e29b-41d4-a716-446655440000",
  "timestamp": "2026-03-05T12:00:00.000Z",
  "payload": { "text": "Добрый день!" }
}
```

| Поле       | Тип     | Описание |
|------------|---------|----------|
| `type`     | string  | Тип события (константа из пакета `events`). |
| `call_id`  | string  | ID звонка (пусто для `ws.connected`). |
| `timestamp`| string  | UTC, RFC3339. |
| `payload`  | object  | Произвольные данные (зависят от `type`). |

Клиент по `type` решает, как обрабатывать событие и что брать из `payload`.

---

## Список событий (текущее состояние)

### Подключение

| type           | Когда | payload |
|----------------|-------|---------|
| `ws.connected` | Клиент подключился к WebSocket. | `{"status":"ready"}` |

### Жизненный цикл звонка

| type            | Когда | payload |
|-----------------|-------|---------|
| `call.started`  | Звонок принят в обработку. | `{"phone_number":"79991234567"}` |
| `call.connecting` | Начало подключения к внешнему сервису. | `{"target":"yandex"}` |
| `call.ended`    | Звонок завершён. | `{"reason":"farewell" \| "abonent_hangup" \| "timeout" \| "cancelled" \| "audiosocket_timeout"}` |
| `call.error`    | Ошибка в процессе звонка. | `{"message":"...", "source":"yandex" \| "asterisk" \| "ws"}` |

### Asterisk

| type                       | Когда | payload |
|----------------------------|-------|---------|
| `asterisk.originate_sent`  | AMI Originate отправлен. | `{"phone_number":"..."}` |
| `asterisk.originate_response` | Ответ на Originate (пока не эмитится из кода). | `{"success", "reason", "channel"}` |
| `asterisk.audiosocket_ready` | AudioSocket от Asterisk подключился. | — |
| `asterisk.hangup`         | Команда завершения звонка отправлена. | — |

### Yandex Realtime

| type                  | Когда | payload |
|-----------------------|-------|---------|
| `yandex.connecting`   | Начало подключения к Yandex. | — |
| `yandex.connected`    | WebSocket к Yandex установлен. | — |
| `yandex.session_ready`| Сессия Realtime готова. | — |
| `yandex.event`        | Произвольное событие от Yandex (если добавить эмит). | `{"event_type", "detail"}` |
| `yandex.text_delta`   | Очередной фрагмент текста ответа. | `{"text":"..."}` |
| `yandex.audio_chunk`  | Получен аудио-чанк от модели. | `{"size_bytes": N}` |
| `yandex.speech_started`  | VAD зафиксировал начало речи абонента. | — |
| `yandex.speech_stopped`  | Конец реплики абонента. | — |
| `yandex.response_done`   | Модель закончила генерировать ответ. | — |

---

## Как добавить новое событие

### 1. Константа и конструктор в `internal/events/events.go`

```go
// В блок const добавить:
MyNewEvent = "mydomain.my_action"

// Конструктор (удобно для типизации payload):
func NewMyNewEvent(callID string, someField string) Event {
    return newEvent(MyNewEvent, callID, map[string]string{"some_field": someField})
}
```

Использовать `newEvent(typ, callID, payload)` для любых payload (в т.ч. вложенных структур через `map[string]any` или свой тип с последующим `json.Marshal`).

### 2. Вызов в месте возникновения

В `CallService` (или другом месте, где есть `events.Emitter`):

```go
em.Emit(events.NewMyNewEvent(callID, value))
```

При вызове через HTTP `/call/start` передаётся `NoopEmitter{}`, поэтому новые события просто не будут никуда отправляться. При вызове через WebSocket передаётся `WSEmitter` — события уйдут клиенту.

### 3. (Опционально) Новая реализация Emitter

Например, пишет в лог или в метрики:

```go
type LogEmitter struct{}
func (LogEmitter) Emit(ev events.Event) {
    log.Printf("[event] %s call_id=%s", ev.Type, ev.CallID)
}
```

Сервис от этого не зависит: ему по-прежнему передаётся интерфейс `events.Emitter`.

---

## Преимущества подхода

1. **Единый контракт** — все события в одном формате (`type`, `call_id`, `timestamp`, `payload`), клиенту достаточно парсить JSON и ветвить по `type`.
2. **Расширяемость** — новый тип события = константа + конструктор + один вызов `em.Emit(...)` в нужном месте. Не нужно менять подписчиков.
3. **Независимость от транспорта** — `CallService` не знает про WebSocket/HTTP; один и тот же код работает с WS (поток событий) и с GET `/call/start` (без событий).
4. **Удобно тестировать** — в тестах передаётся mock Emitter (например, пишет в slice) и проверяется набор событий.
5. **Несколько подписчиков** — при необходимости можно сделать `MultiEmitter`, который держит несколько `Emitter` и при `Emit` вызывает каждый.
6. **Понятная трассировка** — по `call_id` и `timestamp` можно собрать лог или метрики по одному звонку.

---

## Транспорты

| Точка входа        | Emitter        | События клиенту |
|--------------------|----------------|------------------|
| `GET /call/start`  | `NoopEmitter` | Нет              |
| `WS /ws` + команда `start_call` | `WSEmitter` | Да, поток JSON по одному сообщению на событие |

Команды по WebSocket (пока одна):

- `{"action":"start_call","phone_number":"79991234567"}` — инициировать звонок; дальше идёт поток событий до `call.ended` или `call.error`.

---

## Файлы

| Файл | Назначение |
|------|------------|
| `internal/events/events.go` | Константы типов, структура `Event`, интерфейс `Emitter`, конструкторы, `NoopEmitter`. |
| `internal/events/ws_emitter.go` | Отправка событий в WebSocket (thread-safe). |
| `internal/handlers/websocket.go` | Приём WS, отправка `ws.connected`, разбор команд, вызов `HandleCall(..., emitter)`. |
| `internal/service/call_service.go` | Оркестрация звонка и вызовы `em.Emit(...)` в нужных точках. |
