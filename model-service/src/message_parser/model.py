from __future__ import annotations

import os
import re
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo

import joblib
from sklearn.feature_extraction.text import TfidfVectorizer
from sklearn.linear_model import LogisticRegression
from sklearn.pipeline import Pipeline

from message_parser.category_rules import infer_category
from message_parser.data import FIELD_NAMES, NULL_LABEL, decode_label
from message_parser.normalizer import normalize_text
from message_parser.schemas import ParseResponse, ParserOutput
from message_parser.title_extractor import extract_title
from message_parser.time_rules import resolve_time


DEFAULT_BASE_TIME = "2026-06-20T00:45:00+03:00"


@dataclass
class ConstantFieldModel:
    label: str

    def predict(self, texts: list[str]) -> list[str]:
        return [self.label for _ in texts]

    def predict_proba(self, texts: list[str]) -> list[list[float]]:
        return [[1.0] for _ in texts]

    @property
    def classes_(self) -> list[str]:
        return [self.label]


@dataclass
class PlanningParserModel:
    field_models: dict[str, Any]
    version: str
    created_at: str
    train_summary: dict[str, Any]

    def predict(self, text: str, base_time: str | None = None) -> ParseResponse:
        base_dt = parse_base_time(base_time)
        normalized = normalize_text(text)
        raw_fields: dict[str, str | None] = {}
        confidences: dict[str, float] = {}

        for field in FIELD_NAMES:
            label, confidence = predict_field(self.field_models[field], normalized)
            raw_fields[field] = decode_label(label)
            confidences[field] = confidence

        status = raw_fields["status"] or "failed"
        output = ParserOutput(
            title=extract_title(text, raw_fields["title"], confidences.get("title", 0.0)),
            due_at=None,
            remind_at=None,
            priority=raw_fields["priority"],
            category=raw_fields["category"],
            assignee=raw_fields["assignee"],
            repeat=raw_fields["repeat"],
            status=status,
            clarification_reason=raw_fields["clarification_reason"],
        )

        if status in {"ignored", "failed"}:
            output = ParserOutput(status=status)
        else:
            due_at, remind_at, source = self._resolve_datetimes(text, base_dt, raw_fields)
            output.due_at = due_at
            output.remind_at = remind_at
            explicit_assignee = _explicit_assignee(normalized)
            if explicit_assignee is not None:
                output.assignee = explicit_assignee
            explicit_repeat = _explicit_repeat(normalized)
            if explicit_repeat is not None:
                output.repeat = explicit_repeat
            elif output.repeat is not None and not _has_repeat_marker(normalized):
                output.repeat = None
            if output.repeat is not None:
                output.due_at = None
                output.remind_at = None
                source = source if source != "none" else "repeat"
            self._apply_guardrails(text, output, confidences)

        confidence = min(confidences.get("status", 0.0), _mean(confidences.values()))
        return ParseResponse(
            output=output,
            confidence=round(confidence, 4),
            field_confidence={key: round(value, 4) for key, value in confidences.items()},
            source="hybrid",
            time_source=self._time_source(text, base_dt, raw_fields),
            model_version=self.version or "unknown/local",
        )

    def _apply_guardrails(self, text: str, output: ParserOutput, confidences: dict[str, float]) -> None:
        normalized = normalize_text(text)

        explicit_priority = _explicit_priority(normalized)
        if explicit_priority is not None:
            output.priority = explicit_priority

        category_confidence = confidences.get("category", 1.0)
        fallback_category = infer_category(normalized)
        update_reason = _event_update_reason(normalized)
        if update_reason is not None:
            output.status = "needs_clarification"
            output.clarification_reason = update_reason

        if output.status == "success":
            if output.clarification_reason in {"missing_due_at", "vague_due_at"} and output.due_at is None and output.repeat is None:
                output.status = "partial"
            elif output.clarification_reason == "category_uncertain" and output.category == "unknown":
                output.status = "partial"
            elif output.clarification_reason == "assignee_missing" and output.assignee is None:
                output.status = "partial"

        if output.due_at is None:
            output.remind_at = None

        if output.status == "success" and output.due_at is None and output.repeat is None:
            output.status = "partial"
            output.clarification_reason = "missing_due_at"

        if (
            output.status == "needs_clarification"
            and output.clarification_reason is None
            and output.title
            and (output.due_at is not None or output.repeat is not None)
            and not _looks_ambiguous_intent(normalized)
        ):
            output.status = "success"

        if output.clarification_reason == "category_uncertain" and output.title:
            if fallback_category is not None:
                output.category = fallback_category
                output.clarification_reason = None
                if output.due_at is not None or output.repeat is not None:
                    output.status = "success"
            elif output.status == "needs_clarification":
                output.status = "partial"

        if fallback_category is not None and category_confidence < 0.95:
            output.category = fallback_category

        if output.category in {None, "unknown"} and output.clarification_reason != "category_uncertain" and fallback_category is not None:
            output.category = fallback_category

        if output.status == "success":
            output.clarification_reason = None

        if output.status == "partial" and output.clarification_reason == "category_uncertain":
            output.category = "unknown"

        if output.status in {"success", "partial"} and output.priority is None:
            output.priority = _fallback_priority(normalized, output)

        if output.status == "needs_clarification":
            output.due_at = None
            output.remind_at = None
            output.priority = None
            if output.clarification_reason == "multiple_tasks":
                output.category = "unknown"

        if output.status in {"ignored", "failed"}:
            output.title = None
            output.due_at = None
            output.remind_at = None
            output.priority = None
            output.category = None
            output.assignee = None
            output.repeat = None
            output.clarification_reason = None

    def _resolve_datetimes(
        self,
        text: str,
        base_dt: datetime,
        raw_fields: dict[str, str | None],
    ) -> tuple[str | None, str | None, str]:
        rule_result = resolve_time(text, base_dt)
        if rule_result.due_at is not None:
            due = rule_result.due_at
            remind = rule_result.remind_at or (due - timedelta(hours=1))
            return isoformat(due), isoformat(remind), rule_result.source

        due = datetime_from_delta(base_dt, raw_fields.get("due_delta_seconds"))
        if due is None:
            return None, None, "none"
        remind = datetime_from_delta(base_dt, raw_fields.get("remind_delta_seconds"))
        return isoformat(due), isoformat(remind), "model_delta" if due is not None else "none"

    def _time_source(self, text: str, base_dt: datetime, raw_fields: dict[str, str | None]) -> str:
        rule_result = resolve_time(text, base_dt)
        if rule_result.due_at is not None:
            return rule_result.source
        if raw_fields.get("due_delta_seconds") is not None:
            return "model_delta"
        return "none"


def train_field_model(texts: list[str], labels: list[str]) -> Any:
    unique = sorted(set(labels))
    if len(unique) == 1:
        return ConstantFieldModel(unique[0])
    return Pipeline(
        steps=[
            (
                "tfidf",
                TfidfVectorizer(
                    analyzer="char_wb",
                    ngram_range=(3, 5),
                    lowercase=True,
                    min_df=1,
                    max_features=40000,
                ),
            ),
            (
                "classifier",
                LogisticRegression(
                    max_iter=1200,
                    class_weight="balanced",
                    solver="lbfgs",
                ),
            ),
        ]
    ).fit(texts, labels)


def predict_field(model: Any, text: str) -> tuple[str, float]:
    labels = model.predict([text])
    label = str(labels[0])
    if hasattr(model, "predict_proba"):
        probabilities = model.predict_proba([text])[0]
        classes = list(model.classes_ if hasattr(model, "classes_") else model[-1].classes_)
        if label in classes:
            return label, float(probabilities[classes.index(label)])
        return label, float(max(probabilities))
    return label, 1.0


def save_model(model: PlanningParserModel, path: str | Path) -> None:
    model_path = Path(path)
    model_path.parent.mkdir(parents=True, exist_ok=True)
    joblib.dump(model, model_path)


def load_model(path: str | Path) -> PlanningParserModel:
    return joblib.load(Path(path))


def parse_base_time(value: str | None) -> datetime:
    if value is None:
        timezone_name = os.getenv("DEFAULT_TIMEZONE", "Europe/Moscow")
        try:
            return datetime.now(ZoneInfo(timezone_name))
        except Exception:
            return datetime.now(timezone.utc).astimezone()
    return datetime.fromisoformat(value)


def datetime_from_delta(base_dt: datetime, value: str | None) -> datetime | None:
    if value is None or value == NULL_LABEL:
        return None
    try:
        return base_dt + timedelta(seconds=int(value))
    except ValueError:
        return None


def isoformat(value: datetime | None) -> str | None:
    if value is None:
        return None
    return value.isoformat()


def _mean(values: Any) -> float:
    values = list(values)
    if not values:
        return 0.0
    return sum(values) / len(values)


def _explicit_priority(text: str) -> str | None:
    if "не срочно" in text:
        return "p3"
    if "p1" in text or "срочно" in text or "важно" in text:
        return "p1"
    if "p2" in text:
        return "p2"
    if "p3" in text:
        return "p3"
    return None


def _fallback_priority(text: str, output: ParserOutput) -> str:
    if "сегодня" in text or "через" in text or "сейчас" in text:
        return "p1"
    if output.due_at is not None or output.repeat is not None:
        return "p2"
    return "p3"


def _has_repeat_marker(text: str) -> bool:
    return any(marker in text for marker in ("кажд", "раз в", "по будням", "ежеднев", "еженед"))


def _explicit_repeat(text: str) -> str | None:
    if re.search(r"\b(?:каждый месяц|ежемесячно|раз в месяц)\b", text):
        return "RRULE:FREQ=MONTHLY"
    if re.search(r"\bкаждое первое число\b", text):
        return "RRULE:FREQ=MONTHLY;BYMONTHDAY=1"
    if re.search(r"\bраз в две недели\b", text):
        return "RRULE:FREQ=WEEKLY;INTERVAL=2"
    if re.search(r"\bкаждые\s+2\s+дн[яей]*\b", text):
        return "RRULE:FREQ=DAILY;INTERVAL=2"
    if re.search(r"\bкаждые\s+3\s+дн[яей]*\b", text):
        return "RRULE:FREQ=DAILY;INTERVAL=3"
    if re.search(r"\bпо будням(?:\s+в\s+10)?\b", text):
        if re.search(r"\bв\s+10\b", text):
            return "RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=10;BYMINUTE=0"
        return "RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"
    if re.search(r"\bкаждый вторник и четверг\b", text):
        return "RRULE:FREQ=WEEKLY;BYDAY=TU,TH"
    if re.search(r"\bкаждый понедельник\b", text):
        return "RRULE:FREQ=WEEKLY;BYDAY=MO"
    if re.search(r"\b(?:каждый день в 9 утра|каждое утро)\b", text):
        return "RRULE:FREQ=DAILY;BYHOUR=9;BYMINUTE=0"
    if re.search(r"\bкаждый день утром\b", text):
        return "RRULE:FREQ=DAILY;BYHOUR=9;BYMINUTE=0"
    if re.search(r"\bкаждый день вечером\b", text):
        return "RRULE:FREQ=DAILY;BYHOUR=19;BYMINUTE=0"
    if re.search(r"\b(?:каждый день|ежедневно)\b", text):
        return "RRULE:FREQ=DAILY"
    if re.search(r"\bраз в неделю\b", text):
        return "RRULE:FREQ=WEEKLY"
    return None


def _explicit_assignee(text: str) -> str | None:
    assignees = {
        "иван трифонов": "Иван Трифонов",
        "мама": "мама",
        "леша": "Леша",
        "наташа": "Наташа",
        "сергей": "Сергей",
        "родители": "родители",
        "тетя наташа": "тетя Наташа",
        "оля": "Оля",
        "дима": "Дима",
        "сестра": "сестра",
        "бабушка": "бабушка",
    }
    for marker, assignee in sorted(assignees.items(), key=lambda item: len(item[0]), reverse=True):
        if re.search(rf"\b{re.escape(marker)}\b", text):
            return assignee
    return None


def _event_update_reason(text: str) -> str | None:
    if re.search(r"\b(?:перенеси|перенести|перенес|сдвинь|сдвинуть|отмени|отменить|удали|удалить)\b", text):
        return "missing_target_event"
    return None


def _looks_ambiguous_intent(text: str) -> bool:
    if any(marker in text for marker in ("на днях", "когда получится", "когда проснусь", "что-нибудь", "штук")):
        return True
    if "вчера" in text and any(marker in text for marker in ("сегодня", "завтра")):
        return True
    weekday_mentions = re.findall(
        r"\b(?:в|во|до|к|на)\s+(?:понедельник[а]?|вторник[а]?|сред[уаы]?|четверг[а]?|пятниц[уаы]?|суббот[уаы]?|воскресень[ея])\b",
        text,
    )
    return len(weekday_mentions) > 1
