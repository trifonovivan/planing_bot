# planning_ru dataset

Датасет для обучения модели, которая разбирает сообщения пользователя в Telegram-боте планирования.

Каждая строка в `train.jsonl` и `valid.jsonl` — отдельный JSON-объект:

```json
{
  "input": "Завтра утром написать селф-ревью",
  "base_time": "2026-06-20T00:45:00+03:00",
  "output": {
    "title": "написать селф-ревью",
    "due_at": "2026-06-21T09:00:00+03:00",
    "remind_at": "2026-06-21T08:00:00+03:00",
    "priority": "p2",
    "category": "work",
    "assignee": "Иван Трифонов",
    "repeat": null,
    "status": "success",
    "clarification_reason": null
  }
}
```

## Файлы

- `datasets/planning_ru/train.jsonl` — 15000 обучающих примеров.
- `datasets/planning_ru/valid.jsonl` — 2000 валидационных примеров с holdout title/time/base_time и без normalized-дублей из train.
- `scripts/generate_planning_dataset.py` — детерминированный генератор и валидатор.

## Схема output

- `title` — нормализованное название задачи или `null`.
- `due_at` — срок в ISO-8601 или `null`.
- `remind_at` — время напоминания в ISO-8601 или `null`.
- `priority` — `p1`, `p2`, `p3` или `null`.
- `category` — одна из категорий: `work`, `shopping`, `home`, `health`, `finance`, `car`, `study`, `family`, `garden`, `personal`, `unknown`.
- `assignee` — исполнитель или `null`.
- `repeat` — RRULE-подобное правило повторения или `null`.
- `status` — `success`, `partial`, `needs_clarification`, `ignored`, `failed`.
- `clarification_reason` — причина уточнения или частичного разбора.

## Правила времени

Генератор не использует текущее системное время. Вместо одного фиксированного момента
используются детерминированные `base_time`-пулы для train и valid, чтобы проверять
обобщение на новых датах:

```text
train: 2026-06-20T00:45:00+03:00, 2026-06-24T08:10:00+03:00, 2026-07-03T18:35:00+03:00
valid: 2026-07-09T11:20:00+03:00, 2026-08-17T21:05:00+03:00, 2026-11-02T07:50:00+03:00
```

Основные правила:

- `сегодня`, `завтра`, `послезавтра` считаются от `base_time` строки;
- weekday-фразы вроде `на четверг`, `в понедельник`, `до следующей среды` считаются от `base_time` строки;
- `до конца недели` и `до конца месяца` считаются от `base_time` строки;
- `утром` = `09:00`;
- `днем` = `13:00`;
- `после обеда` = `14:00`;
- `вечером` = `19:00`;
- `ночью` = `23:00`;
- `после работы` = `19:00`;
- `перед работой` = `09:00`;
- если срок есть, а напоминание не указано, `remind_at = due_at - 1 час`;
- если срока нет, `due_at = null`, `remind_at = null`.

## Генерация

Из корня проекта:

```bash
python3 scripts/generate_planning_dataset.py --out . --seed 424242 --train 15000 --valid 2000
```

Генератор создаст файлы:

```text
datasets/planning_ru/train.jsonl
datasets/planning_ru/valid.jsonl
```

## Проверка

Валидатор встроен в генератор и запускается после генерации автоматически.

Проверяется:

- валидный JSONL;
- непустой `input`;
- корректный ISO-8601 `base_time`;
- допустимые `status`, `priority`, `category`;
- `success` содержит `title` и либо `due_at`, либо `repeat`;
- `repeat` не смешивается с `due_at`/`remind_at`;
- для `ignored`/`failed` основные поля равны `null`;
- `due_at` и `remind_at` имеют ISO-8601 формат;
- `remind_at` не позже `due_at` и не задан без `due_at`;
- нет пересечений exact и normalized `input` между train и valid;
- normalized-дубликаты не имеют разных labels.

## Как добавлять новые шаблоны

1. Открой `scripts/generate_planning_dataset.py`.
2. Добавь train-задачи в `TASKS` или valid-only задачи в `VALID_TASKS`.
3. Добавь train-временные выражения в `DATE_TIMES` или valid-only выражения в `VALID_DATE_TIMES`.
4. Добавь повторения в `REPEATS` или `VALID_REPEATS`.
5. Для ручных обязательных примеров добавь объект в `hardcoded_examples()`.
6. Запусти генератор и убедись, что проверка прошла.

## Распределение

Целевое распределение по статусам:

- около 45% `success`;
- около 25% `partial`;
- около 15% `needs_clarification`;
- около 15% `ignored`/`failed`.

Датасет содержит нормальные формулировки, разговорную речь, сокращения, опечатки, грамматические ошибки, лишние слова, разный порядок слов, команды бота, мусорные и неоднозначные сообщения.
