from __future__ import annotations

import calendar
import re
from dataclasses import dataclass
from datetime import datetime, timedelta

from message_parser.normalizer import normalize_text


DAY_PARTS = {
    "утром": (9, 0),
    "днем": (13, 0),
    "днём": (13, 0),
    "после обеда": (14, 0),
    "вечером": (19, 0),
    "ночью": (23, 0),
}

MONTHS = {
    "января": 1,
    "февраля": 2,
    "марта": 3,
    "апреля": 4,
    "мая": 5,
    "июня": 6,
    "июля": 7,
    "августа": 8,
    "сентября": 9,
    "октября": 10,
    "ноября": 11,
    "декабря": 12,
}

NUMBER_WORDS = {
    "одну": 1,
    "один": 1,
    "два": 2,
    "две": 2,
    "три": 3,
    "четыре": 4,
    "пять": 5,
}

WEEKDAYS = {
    "понедельник": 0,
    "понедельника": 0,
    "вторник": 1,
    "вторника": 1,
    "среду": 2,
    "среда": 2,
    "среды": 2,
    "четверг": 3,
    "четверга": 3,
    "пятницу": 4,
    "пятница": 4,
    "пятницы": 4,
    "субботу": 5,
    "суббота": 5,
    "субботы": 5,
    "воскресенье": 6,
    "воскресенья": 6,
}


@dataclass(frozen=True)
class TimeResolution:
    due_at: datetime | None = None
    remind_at: datetime | None = None
    source: str = "none"


def resolve_time(text: str, base_time: datetime) -> TimeResolution:
    normalized = normalize_text(text)
    due, source = _resolve_due(normalized, base_time)
    if due is None:
        return TimeResolution(source="none")
    remind = _resolve_reminder(normalized, due)
    if remind is None:
        remind = due - timedelta(hours=1)
    return TimeResolution(due_at=due, remind_at=remind, source=source)


def _resolve_due(text: str, base_time: datetime) -> tuple[datetime | None, str]:
    relative = _relative_due(text, base_time)
    if relative is not None:
        return relative, "relative"

    date_value, date_kind = _date_due(text, base_time)
    if date_value is None:
        day_part_time = _day_part_only_due(text, base_time)
        if day_part_time is not None:
            return day_part_time, "day_part"
        clock_time = _clock_only_due(text, base_time)
        if clock_time is not None:
            return clock_time, "clock"
        work_time = _workday_due(text, base_time)
        if work_time is not None:
            return work_time, "workday"
        return None, "none"

    hour, minute = _explicit_clock(text) or _day_part_clock(text) or _default_clock(date_kind)
    return _with_clock(date_value, hour, minute), date_kind


def _relative_due(text: str, base_time: datetime) -> datetime | None:
    if re.search(r"\b(?:через\s+полчаса|полчаса|минут\s+через\s+30)\b", text):
        return base_time + timedelta(minutes=30)
    minute_later = re.search(r"\bминут\s+через\s+(\d+)\b", text)
    if minute_later is not None:
        return base_time + timedelta(minutes=int(minute_later.group(1)))
    if re.search(r"\bчерез\s+час\b", text):
        return base_time + timedelta(hours=1)

    amount: int
    unit: str
    match = re.search(r"\bчерез\s+(\d+)\s+(минут[а-я]*|час[а-я]*|дн[яей]*|недел[а-я]*|месяц[а-я]*)\b", text)
    if match is None:
        word_match = re.search(r"\bчерез\s+(одну|один|два|две|три|четыре|пять)\s+(минут[а-я]*|час[а-я]*|дн[яей]*|недел[а-я]*|месяц[а-я]*)\b", text)
        if word_match is not None:
            amount = NUMBER_WORDS[word_match.group(1)]
            unit = word_match.group(2)
        else:
            match = re.search(r"\bчерез\s+(неделю|месяц)\b", text)
            if match is None:
                return None
            amount = 1
            unit = match.group(1)
    else:
        amount = int(match.group(1))
        unit = match.group(2)

    if unit.startswith("минут"):
        return base_time + timedelta(minutes=amount)
    if unit.startswith("час"):
        return base_time + timedelta(hours=amount)
    if unit.startswith("д"):
        return _with_clock(base_time + timedelta(days=amount), 13, 0)
    if unit.startswith("недел") or unit == "неделю":
        return _with_clock(base_time + timedelta(weeks=amount), 13, 0)
    if unit.startswith("месяц"):
        return _with_clock(_add_months(base_time, amount), 13, 0)
    return None


def _date_due(text: str, base_time: datetime) -> tuple[datetime | None, str]:
    correction = re.search(r"\bне\s+.+?\s+а\s+(.+)", text)
    if correction is not None:
        corrected_due, corrected_kind = _date_due_uncorrected(correction.group(1), base_time)
        if corrected_due is not None:
            return corrected_due, corrected_kind
    return _date_due_uncorrected(text, base_time)


def _date_due_uncorrected(text: str, base_time: datetime) -> tuple[datetime | None, str]:
    if "до конца недели" in text:
        return base_time + timedelta(days=(6 - base_time.weekday()) % 7), "end_of_week"
    if re.search(r"\b(?:(?:в|к|ко|до)\s+)?конц(?:е|у|а)\s+месяца\b", text):
        last_day = calendar.monthrange(base_time.year, base_time.month)[1]
        return base_time.replace(day=last_day), "end_of_month"

    if "послезавтра" in text or "после завтра" in text:
        return base_time + timedelta(days=2), "date_word"
    if "завтра" in text:
        return base_time + timedelta(days=1), "date_word"
    if "сегодня" in text:
        return base_time, "date_word"

    if "на следующей неделе" in text:
        days = (7 - base_time.weekday()) % 7
        if days == 0:
            days = 7
        return base_time + timedelta(days=days), "next_week"
    if "на будущей неделе" in text or "на неделе" in text:
        days = (7 - base_time.weekday()) % 7
        if days == 0:
            days = 7
        return base_time + timedelta(days=days), "next_week"
    if "к обеду" in text:
        return base_time, "date_word"

    if "на выходных" in text:
        if base_time.weekday() < 5:
            days = 5 - base_time.weekday()
        elif base_time.weekday() == 5:
            days = 1
        else:
            days = 0
        return base_time + timedelta(days=days), "weekend"

    qualified_weekday = re.search(r"\b(?:(в|во|до|к|на)\s+)?(следующ(?:ий|ую|ее|ей)|будущ(?:ий|ую|ее|ей)|эт(?:от|у|о|ой))\s+(понедельник[а]?|вторник[а]?|сред[уаы]?|четверг[а]?|пятниц[уаы]?|суббот[уаы]?|воскресень[ея])\b", text)
    if qualified_weekday is not None:
        preposition = qualified_weekday.group(1)
        qualifier = qualified_weekday.group(2)
        day_name = qualified_weekday.group(3)
        target = WEEKDAYS[day_name]
        days = (target - base_time.weekday()) % 7
        if qualifier.startswith(("след", "будущ")):
            days = days + 7 if days == 0 else days
        kind = "weekday_until" if preposition in {"до", "к"} else "qualified_weekday"
        return base_time + timedelta(days=days), kind

    weekday = re.search(r"\b(в|во|до|к|на)\s+(понедельник[а]?|вторник[а]?|сред[уаы]?|четверг[а]?|пятниц[уаы]?|суббот[уаы]?|воскресень[ея])\b", text)
    if weekday is not None:
        preposition = weekday.group(1)
        day_name = weekday.group(2)
        target = WEEKDAYS[day_name]
        days = (target - base_time.weekday()) % 7
        kind = "weekday_until" if preposition in {"до", "к"} else "weekday"
        return base_time + timedelta(days=days), kind

    iso_match = re.search(r"\b(\d{4})-(\d{2})-(\d{2})\b", text)
    if iso_match is not None:
        year, month, day = [int(part) for part in iso_match.groups()]
        return base_time.replace(year=year, month=month, day=day), "explicit_date"

    dot_match = re.search(r"\b(\d{1,2})\.(\d{1,2})(?:\.(\d{2,4}))?\b", text)
    if dot_match is not None:
        day = int(dot_match.group(1))
        month = int(dot_match.group(2))
        year = _normalize_year(dot_match.group(3), base_time.year)
        return _future_date(base_time, year, month, day), "explicit_date"

    month_match = re.search(r"\b(\d{1,2})(?:-?го)?\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)\b", text)
    if month_match is not None:
        day = int(month_match.group(1))
        month = MONTHS[month_match.group(2)]
        return _future_date(base_time, base_time.year, month, day), "explicit_date"

    return None, "none"


def _workday_due(text: str, base_time: datetime) -> datetime | None:
    if "после работы" in text:
        return _with_clock(base_time, 19, 0)
    if "перед работой" in text:
        return _with_clock(base_time, 9, 0)
    return None


def _day_part_only_due(text: str, base_time: datetime) -> datetime | None:
    clock = _day_part_clock(text)
    if clock is None:
        return None
    due = _with_clock(base_time, clock[0], clock[1])
    if due <= base_time:
        due = due + timedelta(days=1)
    return due


def _clock_only_due(text: str, base_time: datetime) -> datetime | None:
    clock = _explicit_clock(text)
    if clock is None:
        return None
    due = _with_clock(base_time, clock[0], clock[1])
    if due <= base_time:
        due = due + timedelta(days=1)
    return due


def _resolve_reminder(text: str, due_at: datetime) -> datetime | None:
    match = re.search(r"\bнапомни\s+за\s+(\d+)\s+(минут[а-я]*|час[а-я]*|дн[яей]*)\b", text)
    if match is not None:
        amount = int(match.group(1))
        unit = match.group(2)
        if unit.startswith("минут"):
            return due_at - timedelta(minutes=amount)
        if unit.startswith("час"):
            return due_at - timedelta(hours=amount)
        if unit.startswith("д"):
            return due_at - timedelta(days=amount)

    if re.search(r"\bнапомни\s+за\s+час\b", text):
        return due_at - timedelta(hours=1)
    if re.search(r"\bнапомни\s+за\s+день\b", text):
        return due_at - timedelta(days=1)
    return None


def _explicit_clock(text: str) -> tuple[int, int] | None:
    match = re.search(r"\b(?:в|к|до)?\s*(\d{1,2})[:.-](\d{2})\b", text)
    if match is not None:
        return int(match.group(1)), int(match.group(2))

    match = re.search(r"\b(?:в|к|до)\s+(\d{1,2})\s*(утра|вечера|дня|ночи)?\b", text)
    if match is None:
        return None
    hour = int(match.group(1))
    part = match.group(2)
    if part in {"вечера", "дня"} and hour < 12:
        hour += 12
    if part == "ночи" and hour == 12:
        hour = 0
    return hour, 0


def _day_part_clock(text: str) -> tuple[int, int] | None:
    for phrase, clock in DAY_PARTS.items():
        if phrase in text:
            return clock
    return None


def _default_clock(kind: str) -> tuple[int, int]:
    if kind in {"end_of_week", "end_of_month", "weekday_until"}:
        return 23, 59
    return 13, 0


def _with_clock(value: datetime, hour: int, minute: int) -> datetime:
    return value.replace(hour=hour, minute=minute, second=0, microsecond=0)


def _future_date(base_time: datetime, year: int, month: int, day: int) -> datetime:
    candidate = base_time.replace(year=year, month=month, day=day)
    if candidate.date() < base_time.date():
        candidate = candidate.replace(year=candidate.year + 1)
    return candidate


def _normalize_year(value: str | None, fallback: int) -> int:
    if value is None:
        return fallback
    year = int(value)
    if year < 100:
        return 2000 + year
    return year


def _add_months(value: datetime, amount: int) -> datetime:
    month_index = value.month - 1 + amount
    year = value.year + month_index // 12
    month = month_index % 12 + 1
    day = min(value.day, calendar.monthrange(year, month)[1])
    return value.replace(year=year, month=month, day=day)
