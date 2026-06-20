# Message Parser Model Service

Python service for parsing Russian planning messages into the bot task schema.

The service is intentionally kept separate from the Go bot runtime:

- training can use heavier Python dependencies;
- the Go bot can keep its existing rule-based parser as a fallback;
- trained artifacts can be uploaded to the server independently.

## Dataset

The expected JSONL format is:

```json
{"input":"Завтра купить молоко","base_time":"2026-06-20T00:45:00+03:00","output":{"title":"купить молоко","due_at":"2026-06-21T13:00:00+03:00","remind_at":"2026-06-21T12:00:00+03:00","priority":"p2","category":"shopping","assignee":"Иван Трифонов","repeat":null,"status":"success","clarification_reason":null}}
```

Local datasets can live under `model-service/datasets/planning_ru/`. JSONL files are ignored by git.

## Train

From the repository root:

```bash
cd model-service
python3 -m venv .venv
. .venv/bin/activate
pip install -e ".[dev]"
python -m message_parser.train \
  --train-file datasets/planning_ru/train.jsonl \
  --valid-file datasets/planning_ru/valid.jsonl \
  --model-out artifacts/planning_ru_model.joblib
```

If your local `pip` is old and editable install fails, use `pip install ".[dev]"`.

Current local validation after generating `15000/2000` train/valid rows with holdout titles,
time phrases, assignees and base times:

```json
{
  "exact_match": 0.6065,
  "due_at": 0.9195,
  "remind_at": 0.922,
  "title": 0.821,
  "category": 0.8945,
  "repeat": 0.9955
}
```

## Evaluate

```bash
python -m message_parser.evaluate \
  --model artifacts/planning_ru_model.joblib \
  --valid-file datasets/planning_ru/valid.jsonl
```

## Predict

```bash
python -m message_parser.predict \
  --model artifacts/planning_ru_model.joblib \
  --text "завтра вечером купить молоко"
```

## Run API

```bash
MODEL_PATH=artifacts/planning_ru_model.joblib \
uvicorn message_parser.api:app --host 0.0.0.0 --port 8090
```

Request:

```bash
curl -s http://localhost:8090/parse \
  -H 'content-type: application/json' \
  -d '{"text":"завтра вечером купить молоко"}'
```

## Docker

```bash
docker build -t planner-parser-model ./model-service
docker run --rm -p 8090:8090 \
  -v "$PWD/model-service/artifacts:/app/artifacts:ro" \
  -e MODEL_PATH=/app/artifacts/planning_ru_model.joblib \
  planner-parser-model
```

## Deployment

Training is wired into a separate GitHub Actions workflow on purpose. Model training is a data job: it is isolated from the app deploy pipeline, reviewed by evaluation metrics, and only then uploaded.

Typical upload after local training, if you need to do it outside GitHub Actions:

```bash
scp model-service/artifacts/planning_ru_model.joblib <user>@<host>:/opt/planner-bot/model-service/artifacts/
```

Then restart only the model service container on the server:

```bash
cd /opt/planner-bot
docker compose restart parser-model
```

Preferred production flow:

1. Open GitHub Actions.
2. Run `Model train`.
3. Review `evaluation.json`.
4. Re-run with `deploy_to_server=true` when the model is good enough for production.

That workflow uploads `model-service`, uploads the trained artifact, rebuilds the
`parser-model` image, and restarts only that container.
