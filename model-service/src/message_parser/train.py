from __future__ import annotations

import argparse
import json
from datetime import datetime, timezone

from message_parser.data import FIELD_NAMES, read_jsonl, rows_to_training_data
from message_parser.model import PlanningParserModel, save_model, train_field_model
from message_parser.normalizer import normalize_text


def train_model(train_file: str, valid_file: str | None, model_out: str) -> PlanningParserModel:
    train_rows = read_jsonl(train_file)
    texts, labels_by_field = rows_to_training_data(train_rows)
    normalized_texts = [normalize_text(text) for text in texts]

    field_models = {}
    field_class_counts = {}
    for field in FIELD_NAMES:
        field_models[field] = train_field_model(normalized_texts, labels_by_field[field])
        field_class_counts[field] = len(set(labels_by_field[field]))

    summary = {
        "train_rows": len(train_rows),
        "valid_rows": len(read_jsonl(valid_file)) if valid_file else 0,
        "field_class_counts": field_class_counts,
    }
    model = PlanningParserModel(
        field_models=field_models,
        version="0.1.0",
        created_at=datetime.now(timezone.utc).isoformat(),
        train_summary=summary,
    )
    save_model(model, model_out)
    return model


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--train-file", required=True)
    parser.add_argument("--valid-file")
    parser.add_argument("--model-out", default="artifacts/planning_ru_model.joblib")
    args = parser.parse_args()

    model = train_model(args.train_file, args.valid_file, args.model_out)
    print(json.dumps(model.train_summary, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
