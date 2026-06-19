from __future__ import annotations

import re

from message_parser.normalizer import normalize_text


PREFIX_RE = re.compile(
    r"\b(слушай|короче|так|бот|плиз|плз|пожалуйста|пж|надо|нужно|не забыть|"
    r"блин не забыть бы|закинь в задачи|напомни|важно|срочно|не срочно|p[123])\b",
    flags=re.IGNORECASE,
)

TIME_PATTERNS = [
    r"\bсегодня(?:\s+(?:утром|днем|днём|после обеда|вечером|ночью))?\b",
    r"\bзавтра(?:\s+(?:утром|днем|днём|после обеда|вечером|ночью))?\b",
    r"\bпослезавтра(?:\s+(?:утром|днем|днём|после обеда|вечером|ночью))?\b",
    r"\b(?:в|во|до)\s+(?:понедельник[а]?|вторник[а]?|сред[уаы]?|четверг[а]?|пятниц[уаы]?|суббот[уаы]?|воскресень[ея])(?:\s+(?:утром|днем|днём|после обеда|вечером|ночью))?\b",
    r"\bдо конца (?:недели|месяца)\b",
    r"\bна выходных\b",
    r"\bна следующей неделе\b",
    r"\bна будущей неделе\b",
    r"\bна неделе\b",
    r"\bк обеду\b",
    r"\bпосле работы\b",
    r"\bперед работой\b",
    r"\b(?:через\s+полчаса|полчаса|минут\s+через\s+\d+)\b",
    r"\bчерез\s+(?:\d+\s+)?(?:минут[а-я]*|час[а-я]*|дн[яей]*|недел[а-я]*|месяц[а-я]*|час|неделю|месяц)\b",
    r"\b\d{4}-\d{2}-\d{2}\b",
    r"\b\d{1,2}\.\d{1,2}(?:\.\d{2,4})?\b",
    r"\b\d{1,2}\s+(?:января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)\b",
    r"\b(?:в|к|до)?\s*\d{1,2}[:.-]\d{2}\b",
    r"\b(?:в|к|до)\s+\d{1,2}\s*(?:утра|вечера|дня|ночи)?\b",
    r"\b(?:утром|днем|днём|после обеда|вечером|ночью)\b",
    r"\bнапомни\s+за\s+(?:\d+\s+)?(?:минут[а-я]*|час[а-я]*|дн[яей]*|день)\b",
    r"\bкажд(?:ый|ое|ые)\s+[а-я0-9 ]+\b",
    r"\bраз в неделю\b",
    r"\bпо будням(?:\s+в\s+\d{1,2})?\b",
]

ASSIGNEE_HINTS = (
    "иван трифонов",
    "мама",
    "леша",
    "наташа",
    "сергей",
    "родители",
    "тетя наташа",
)


def extract_title(text: str, predicted_title: str | None, title_confidence: float) -> str | None:
    if predicted_title and title_confidence >= 0.55:
        return predicted_title

    candidate = normalize_text(text)
    candidate = re.sub(r"#\d+", " ", candidate)
    for assignee in sorted(ASSIGNEE_HINTS, key=len, reverse=True):
        candidate = re.sub(rf"\b{re.escape(assignee)}\b", " ", candidate)
    for pattern in TIME_PATTERNS:
        candidate = re.sub(pattern, " ", candidate, flags=re.IGNORECASE)
    candidate = PREFIX_RE.sub(" ", candidate)
    candidate = re.sub(r"\b(ок|только|когда сможешь|пж)\b", " ", candidate)
    candidate = re.sub(r"\s+", " ", candidate).strip(" ,.-")
    if not candidate:
        return predicted_title
    return candidate
