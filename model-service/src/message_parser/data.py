from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any, Iterable


NULL_LABEL = "__NULL__"

FIELD_NAMES = (
    "status",
    "priority",
    "category",
    "assignee",
    "repeat",
    "clarification_reason",
    "title",
    "due_delta_seconds",
    "remind_delta_seconds",
)


@dataclass(frozen=True)
class DatasetRow:
    text: str
    base_time: datetime
    output: dict[str, Any]


def read_jsonl(path: str | Path) -> list[DatasetRow]:
    rows: list[DatasetRow] = []
    file_path = Path(path)
    for lineno, line in enumerate(file_path.read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        try:
            obj = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f"{file_path}:{lineno}: invalid json: {exc}") from exc
        rows.append(_row_from_object(obj, file_path, lineno))
    return rows


def make_labels(row: DatasetRow) -> dict[str, str]:
    output = row.output
    due_at = parse_dt(output.get("due_at"))
    remind_at = parse_dt(output.get("remind_at"))

    labels = {
        "status": encode_label(output.get("status")),
        "priority": encode_label(output.get("priority")),
        "category": encode_label(output.get("category")),
        "assignee": encode_label(output.get("assignee")),
        "repeat": encode_label(output.get("repeat")),
        "clarification_reason": encode_label(output.get("clarification_reason")),
        "title": encode_label(output.get("title")),
        "due_delta_seconds": NULL_LABEL,
        "remind_delta_seconds": NULL_LABEL,
    }

    if due_at is not None:
        labels["due_delta_seconds"] = str(int((due_at - row.base_time).total_seconds()))
    if remind_at is not None:
        labels["remind_delta_seconds"] = str(int((remind_at - row.base_time).total_seconds()))
    return labels


def rows_to_training_data(rows: Iterable[DatasetRow]) -> tuple[list[str], dict[str, list[str]]]:
    texts: list[str] = []
    labels_by_field = {field: [] for field in FIELD_NAMES}
    for row in rows:
        texts.append(row.text)
        labels = make_labels(row)
        for field in FIELD_NAMES:
            labels_by_field[field].append(labels[field])
    return texts, labels_by_field


def encode_label(value: Any) -> str:
    if value is None:
        return NULL_LABEL
    return str(value)


def decode_label(value: str) -> str | None:
    if value == NULL_LABEL:
        return None
    return value


def parse_dt(value: Any) -> datetime | None:
    if value is None:
        return None
    if not isinstance(value, str):
        raise TypeError(f"datetime value must be string or null, got {type(value)!r}")
    return datetime.fromisoformat(value)


def _row_from_object(obj: dict[str, Any], path: Path, lineno: int) -> DatasetRow:
    text = obj.get("input")
    if not isinstance(text, str) or not text.strip():
        raise ValueError(f"{path}:{lineno}: input must be a non-empty string")
    base_time = obj.get("base_time")
    if not isinstance(base_time, str):
        raise ValueError(f"{path}:{lineno}: base_time must be a string")
    output = obj.get("output")
    if not isinstance(output, dict):
        raise ValueError(f"{path}:{lineno}: output must be an object")
    return DatasetRow(text=text, base_time=datetime.fromisoformat(base_time), output=output)
