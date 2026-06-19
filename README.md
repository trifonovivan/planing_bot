# Telegram Task Planner Bot

MVP Telegram-бота для планирования задач на Go.

Бот умеет:

- регистрировать пользователя через `/start`
- создавать personal workspace
- создавать задачи из обычного текста
- разбирать русские даты, время, приоритеты и простые категории
- показывать `/today` и `/week`
- закрывать, отменять и переносить задачи inline-кнопками
- поддерживать ежедневные повторяющиеся задачи: `каждый день`, `ежедневно`, `каждое утро`, `каждый вечер`
- отправлять reminders и утренний digest
- отдавать Prometheus-compatible метрики на `/metrics`
- писать JSON logs в stdout

## Стек

- Go
- PostgreSQL
- Docker Compose
- Telegram Bot API через стандартный `net/http`
- SQL-миграции

## Env

Скопируй пример и заполни токен:

```bash
cp .env.example .env
```

Переменные:

```text
BOT_TOKEN=
BOT_TOKEN_FILE=
POSTGRES_USER=planner
POSTGRES_PASSWORD=change_me
POSTGRES_DB=planner
DATABASE_URL=postgres://planner:change_me@postgres:5432/planner?sslmode=disable
DATABASE_URL_FILE=
APP_ENV=local
DEFAULT_TIMEZONE=Europe/Moscow
DIGEST_TIME=09:30
METRICS_ENABLED=true
METRICS_ADDR=:8080
```

`BOT_TOKEN` берется у BotFather. Реальные секреты хранятся только в локальном `.env` или в файлах, переданных через `BOT_TOKEN_FILE` / `DATABASE_URL_FILE`.

В коде нет дефолтов для `BOT_TOKEN` и `DATABASE_URL`: приложение не стартует, пока они явно не заданы.

## Локальный запуск через Docker Compose

```bash
docker compose up --build
```

Compose поднимает:

- `postgres`
- `migrate`, который применяет все `migrations/*.up.sql` по порядку
- `app`

## Локальный запуск без app-контейнера

Поднять только Postgres:

```bash
docker compose up postgres -d
```

Применить миграцию:

```bash
set -a
source .env
set +a
for f in migrations/*.up.sql; do PGPASSWORD="$POSTGRES_PASSWORD" psql -h localhost -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -f "$f"; done
```

Запустить бота:

```bash
export BOT_TOKEN="<telegram-bot-token>"
export DATABASE_URL="postgres://planner:<password>@localhost:5432/planner?sslmode=disable"
export DEFAULT_TIMEZONE="Europe/Moscow"
export DIGEST_TIME="09:30"
go run ./cmd/bot
```

## Тесты

```bash
go test ./...
```

Есть unit-тесты parser'а на обязательные сценарии и service-layer тест без Telegram API/Postgres.

## CI/CD

GitHub Actions workflow лежит в `.github/workflows/ci-cd.yml`.

На `pull_request` в `main` workflow:

- ставит Go-версию из `go.mod`
- скачивает зависимости
- запускает `go test ./...`
- запускает `go vet ./...`
- проверяет сборку Docker-образа

На `push` в `main` или git tag вида `v*.*.*` workflow дополнительно публикует Docker-образ в GHCR:

```text
ghcr.io/trifonovivan/planing_bot:main
ghcr.io/trifonovivan/planing_bot:latest
ghcr.io/trifonovivan/planing_bot:sha-<commit>
```

CD включается отдельно через repository variable:

```text
DEPLOY_ENABLED=true
```

Когда `DEPLOY_ENABLED=true`, push в `main` деплоит сервис по SSH на сервер с установленными Docker и Docker Compose plugin. Workflow копирует исходники, обновляет `.env`, собирает образ на сервере и выполняет `docker compose up -d --build --remove-orphans`.

GitHub Secrets для деплоя:

```text
DEPLOY_HOST=<server-host>
DEPLOY_USER=<ssh-user>
DEPLOY_SSH_KEY=<private-ssh-key>
DEPLOY_PORT=22
DEPLOY_PATH=/opt/planner-bot
BOT_TOKEN=<telegram-bot-token>
POSTGRES_PASSWORD=<postgres-password>
DATABASE_URL=postgres://planner:<postgres-password>@postgres:5432/planner?sslmode=disable
POSTGRES_USER=planner
POSTGRES_DB=planner
```

Опциональные GitHub Variables:

```text
DEFAULT_TIMEZONE=Europe/Moscow
DIGEST_TIME=09:30
METRICS_ENABLED=true
METRICS_BIND=127.0.0.1:8080
```

Если нужно проверить production compose локально из корня репозитория:

```bash
IMAGE=ghcr.io/trifonovivan/planing_bot:main \
BOT_TOKEN=dummy \
POSTGRES_PASSWORD=dummy \
DATABASE_URL='postgres://planner:dummy@postgres:5432/planner?sslmode=disable' \
BUILD_CONTEXT=.. \
MIGRATIONS_DIR=../migrations \
docker compose -f deploy/docker-compose.prod.yml config
```

## Метрики

Метрики включены по умолчанию:

```text
METRICS_ENABLED=true
METRICS_ADDR=:8080
```

Endpoint:

```text
GET /metrics
```

Counters:

```text
task_created_total{workspace_id,user_id,priority,category}
task_done_total{workspace_id,user_id,priority,category}
task_postponed_total{workspace_id,user_id,priority,category}
task_cancelled_total{workspace_id,user_id,priority,category}
reminder_sent_total{workspace_id,user_id}
digest_sent_total{user_id}
telegram_update_total{type}
telegram_callback_total{action}
parser_success_total
parser_error_total{reason}
storage_error_total{operation}
telegram_send_error_total{operation}
```

Gauges:

```text
tasks_active_total{workspace_id}
tasks_overdue_total{workspace_id}
tasks_due_today_total{workspace_id}
reminders_pending_total
```

Histograms:

```text
telegram_update_duration_seconds
parser_duration_seconds
storage_query_duration_seconds{operation}
scheduler_iteration_duration_seconds
```

Для MVP labels `user_id` и `workspace_id` оставлены включенными. TODO: добавить config-флаг для отключения этих labels, если cardinality начнет расти.

## Логи

Логи пишутся в JSON-формате в stdout. Основные события: `task_created`, `done_task`, `postpone_task`, `cancel_task`, `reminder_sent`, `digest_sent`, `parser_failed`, `telegram_send_failed`.

## Хранение данных

Данные не удаляются из приложения hard delete'ом, потому что могут использоваться дальше для аналитики и обучения моделей.

- отмена задачи переводит ее в `cancelled` и заполняет `cancelled_at`
- выполнение задачи переводит ее в `done` и заполняет `done_at`
- выполнение повторяющейся задачи не закрывает всю задачу, а сдвигает следующий reminder и пишет событие выполнения текущего экземпляра
- перенос задачи сохраняет историю в `task_events` и увеличивает `postponed_count`
- внешние ключи в БД используют `ON DELETE RESTRICT`, чтобы случайный delete пользователя/workspace/task не удалил связанные данные каскадом
- `task_id` и `title` не используются в labels метрик

Файл `migrations/001_init.down.sql` остается только для полного сброса локального dev-окружения. В production-подобных окружениях его нельзя применять без явного решения о потере данных.

## Примеры сообщений

```text
завтра купить корм
сегодня в 18:00 полить огурцы
через 3 дня оплатить интернет
через 2 часа проверить задачу
25 июня в 12:00 врач
25.06.2026 в 09:30 созвон
2026-06-25 09:30 встреча
в пятницу вечером купить продукты
на выходных полить теплицу
срочно оплатить ипотеку
не срочно посмотреть PostgreSQL internals
когда-нибудь изучить ClickHouse
Нужно поливать петунии каждый день
каждый вечер полить петунии
```

## Структура

```text
cmd/bot/main.go
internal/config
internal/domain
internal/parser
internal/scheduler
internal/service
internal/storage/postgres
internal/telegram
migrations
docker-compose.yml
.env.example
```
