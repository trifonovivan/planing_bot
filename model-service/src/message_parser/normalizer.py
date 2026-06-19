from __future__ import annotations

import re


TYPO_REPLACEMENTS = {
    "cегодня": "сегодня",
    "завтро": "завтра",
    "завтр": "завтра",
    "завтраа": "завтра",
    "завтрааа": "завтра",
    "севодня": "сегодня",
    "седня": "сегодня",
    "сення": "сегодня",
    "воскрисенье": "воскресенье",
    "работои": "работой",
    "малако": "молоко",
    "молокл": "молоко",
    "инет": "интернет",
    "ипатека": "ипотека",
    "заплотить": "заплатить",
    "стомотолог": "стоматолог",
    "крч": "короче",
    "пжлст": "пожалуйста",
    "щас": "сейчас",
    "ща": "сейчас",
    "вечерком": "вечером",
}


def normalize_text(text: str) -> str:
    lowered = text.lower().replace("ё", "е")
    lowered = _collapse_repeated_letters(lowered)
    lowered = re.sub(r"#\d+", " ", lowered)
    lowered = lowered.replace("/", ".")
    lowered = lowered.replace("-", "-")
    lowered = re.sub(r"[^\w\s:/.-]", " ", lowered, flags=re.UNICODE)
    for typo, replacement in TYPO_REPLACEMENTS.items():
        lowered = lowered.replace(typo, replacement)
    return re.sub(r"\s+", " ", lowered).strip()


def _collapse_repeated_letters(text: str) -> str:
    return re.sub(r"([а-яa-z])\1{2,}", r"\1", text, flags=re.IGNORECASE)
