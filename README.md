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
- связывать профили и ставить задачи связанным пользователям по алиасам
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
BOT_USERNAME=
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
ML_PARSER_URL=http://parser-model:8090/parse
```

`BOT_TOKEN` берется у BotFather. Реальные секреты хранятся только в локальном `.env` или в файлах, переданных через `BOT_TOKEN_FILE` / `DATABASE_URL_FILE`.
`BOT_USERNAME` опционален: если он задан, бот генерирует invite-ссылки вида `https://t.me/<bot>?start=link_<token>`. Если не задан, бот попробует определить username через Telegram `getMe`; если Telegram API недоступен, покажет код `link_<token>`, который можно принять через `/accept`.

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
export BOT_USERNAME="<telegram-bot-username>"
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

## ML parser service

Python-модель разбора сообщений живет отдельно от Go-бота в `model-service/`.
Сервис поднимается как отдельный контейнер `parser-model` и слушает `8090`.
Go-бот использует модель, когда задан `ML_PARSER_URL`; в Docker Compose это включено по умолчанию через `http://parser-model:8090/parse`.
Если ML-сервис недоступен или возвращает ошибку, бот автоматически откатывается на встроенный rule-based parser.

Обучение локально:

```bash
cd model-service
python3 -m venv .venv
. .venv/bin/activate
pip install ".[dev]"
python scripts/generate_planning_dataset.py --out . --seed 424242 --train 10000 --valid 1200
PYTHONPATH=src python -m message_parser.train --train-file datasets/planning_ru/train.jsonl --valid-file datasets/planning_ru/valid.jsonl --model-out artifacts/planning_ru_model.joblib
PYTHONPATH=src python -m message_parser.evaluate --model artifacts/planning_ru_model.joblib --valid-file datasets/planning_ru/valid.jsonl
```

Локальный API:

```bash
cd model-service
MODEL_PATH=artifacts/planning_ru_model.joblib uvicorn message_parser.api:app --host 0.0.0.0 --port 8090
```

Для production compose при запуске из корня репозитория укажи пути:

```bash
BUILD_CONTEXT=.. \
MIGRATIONS_DIR=../migrations \
PARSER_MODEL_CONTEXT=../model-service \
PARSER_MODEL_ARTIFACTS_DIR=../model-service/artifacts \
docker compose -f deploy/docker-compose.prod.yml config
```

## CI/CD

GitHub Actions разнесены на независимые workflows:

- `CI` (`.github/workflows/ci.yml`) проверяет Go-приложение и сборку app Docker image
- `Model train` (`.github/workflows/model-train.yml`) проверяет, обучает и оценивает ML parser model
- `Deploy` (`.github/workflows/deploy.yml`) выкладывает приложение на VPS после успешного `CI`

`CI` запускается на `pull_request` и `push` в `main`, когда меняется Go-приложение, миграции, Docker/compose или deploy-конфиги:

- ставит Go-версию из `go.mod`
- скачивает зависимости
- запускает `go test ./...`
- запускает `go vet ./...`
- проверяет сборку Docker-образа

`Model train` запускается отдельно от app deploy:

- на `pull_request`/`push` с изменениями в `model-service/**`
- вручную через GitHub Actions `Run workflow`
- генерирует synthetic dataset
- запускает Python-тесты
- обучает `artifacts/planning_ru_model.joblib`
- прогоняет evaluation и проверяет `exact_match` против порога
- сохраняет `.joblib` и `evaluation.json` как GitHub artifact
- при ручном запуске с `deploy_to_server=true` загружает `model-service` source и `.joblib` на VPS, пересобирает и перезапускает только `parser-model`

Сам `.joblib` в git не хранится.

Пример ручного переобучения без деплоя модели:

```text
Actions -> Model train -> Run workflow
train_rows=10000
valid_rows=1200
seed=424242
min_exact_match=0.55
deploy_to_server=false
```

CD включается отдельно через repository variable:

```text
DEPLOY_ENABLED=true
```

Когда `DEPLOY_ENABLED=true`, успешный `CI` после `push` в `main` деплоит сервис по SSH на сервер с установленными Docker и Docker Compose plugin. Workflow копирует исходники, обновляет `.env`, собирает образ на сервере и выполняет `docker compose up -d --build --force-recreate --remove-orphans`.

Deploy больше не обучает модель. На сервере должен существовать `model-service/artifacts/planning_ru_model.joblib`; обновление этого файла делает отдельный workflow `Model train`.

Production compose дополнительно поднимает observability-контур:

- `nginx` reverse proxy на `:80`
- `prometheus` для сбора метрик приложения
- `grafana` для дашбордов
- `loki` для хранения логов
- `promtail` для доставки Docker logs в Loki
- `node-exporter` для метрик сервера: CPU, RAM, disk, network, load average
- `cadvisor` для метрик Docker containers

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
GRAFANA_ADMIN_PASSWORD=<grafana-admin-password>
MONITORING_HTPASSWD=<htpasswd-line-for-nginx-basic-auth>
```

Опциональные GitHub Variables:

```text
DEFAULT_TIMEZONE=Europe/Moscow
DIGEST_TIME=09:30
METRICS_ENABLED=true
METRICS_BIND=127.0.0.1:8080
ML_PARSER_URL=http://parser-model:8090/parse
PARSER_MODEL_BIND=127.0.0.1:8090
PUBLIC_HOST=<server-host-or-domain>
HTTP_BIND=0.0.0.0:80
GRAFANA_ADMIN_USER=admin
PROMETHEUS_RETENTION=15d
LOKI_RETENTION=168h
NODE_EXPORTER_VERSION=latest
CADVISOR_VERSION=latest
MODEL_MIN_EXACT_MATCH=0.55
```

Если нужно проверить production compose локально из корня репозитория:

```bash
BOT_TOKEN=dummy \
POSTGRES_PASSWORD=dummy \
DATABASE_URL='postgres://planner:dummy@postgres:5432/planner?sslmode=disable' \
GRAFANA_ADMIN_PASSWORD=dummy \
PUBLIC_HOST=127.0.0.1 \
docker compose --project-directory . -f deploy/docker-compose.prod.yml config
```

## Production endpoints

После деплоя nginx публикует:

```text
GET /health
GET /grafana/
GET /logs
GET /metrics
GET /prometheus/
```

`/grafana/` открывает Grafana. `/logs` ведет в Grafana Explore с Loki datasource.

В Grafana автоматически provisionятся два dashboard:

- `Обзор Planner Bot`
- `Infrastructure Overview`
- `Продукт: задачи`
- `Продукт: доставка и Telegram`
- `Продукт: качество и задержки`

В dashboard'ах `Продукт: доставка и Telegram` и `Продукт: качество и задержки` есть переменная `Перцентиль задержек`. Через нее можно переключать latency-панели между `p50`, `p90`, `p95`, `p99` и `p999`.

`/metrics` и `/prometheus/` закрыты nginx basic auth через `MONITORING_HTPASSWD`, чтобы сырые метрики и Prometheus UI не были публичными.

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
ml_parser_request_total{result,status,time_source}
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
ml_parser_request_duration_seconds{result,status,time_source}
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

## Связка профилей

Пользователь создает invite и сразу указывает, как будет называть второго человека:

```text
/link мама, мам, Таня
```

Бот сгенерирует deep-link вида `https://t.me/<bot>?start=link_<token>`. Если username бота не удалось определить, он покажет код для ручного принятия. Второй пользователь открывает ссылку и указывает свои алиасы для пригласившего:

```text
/accept <token> Ваня, Иван, сын
```

После принятия resolver использует персональный словарь алиасов и типовые падежные формы:

```text
поставь маме задачу купить молоко
маме нужно оплатить интернет
Ваня, купи на Ozon чай https://Ozon.ru/Product/ABC123
купить маме подарок на ДР
разбудить Ваню в 10 утра
```

Если алиас найден, но роль непонятна, бот спросит, кому поставить задачу.

Посмотреть активные связки и алиасы:

```text
/links
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
