from __future__ import annotations

import argparse
import json
from collections import Counter
from typing import Any

from message_parser.data import read_jsonl
from message_parser.model import load_model
from message_parser.schemas import ParserOutput


def evaluate_model(model_path: str, valid_file: str) -> dict[str, Any]:
    model = load_model(model_path)
    rows = read_jsonl(valid_file)

    exact = 0
    field_correct = Counter()
    field_total = Counter()
    status_confusion = Counter()

    for row in rows:
        prediction = model.predict(row.text, row.base_time.isoformat()).output
        predicted = _output_dict(prediction)
        expected = _expected_dict(row.output)
        if predicted == expected:
            exact += 1
        status_confusion[(expected["status"], predicted["status"])] += 1
        for key, expected_value in expected.items():
            field_total[key] += 1
            if predicted.get(key) == expected_value:
                field_correct[key] += 1

    fields = {
        key: round(field_correct[key] / field_total[key], 4)
        for key in sorted(field_total)
    }
    return {
        "rows": len(rows),
        "exact_match": round(exact / len(rows), 4) if rows else 0.0,
        "fields": fields,
        "status_confusion": {
            f"{expected}->{predicted}": count
            for (expected, predicted), count in sorted(status_confusion.items())
        },
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True)
    parser.add_argument("--valid-file", required=True)
    args = parser.parse_args()
    print(json.dumps(evaluate_model(args.model, args.valid_file), ensure_ascii=False, indent=2))


def _output_dict(output: ParserOutput) -> dict[str, Any]:
    return output.model_dump()


def _expected_dict(output: dict[str, Any]) -> dict[str, Any]:
    return {
        "title": output.get("title"),
        "due_at": output.get("due_at"),
        "remind_at": output.get("remind_at"),
        "priority": output.get("priority"),
        "category": output.get("category"),
        "assignee": output.get("assignee"),
        "repeat": output.get("repeat"),
        "status": output.get("status"),
        "clarification_reason": output.get("clarification_reason"),
    }


if __name__ == "__main__":
    main()
