#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Deterministic Russian Telegram planning-bot dataset generator.
Creates:
  datasets/planning_ru/train.jsonl
  datasets/planning_ru/valid.jsonl
and validates schema/quality constraints.
"""
from __future__ import annotations

import argparse
import json
import random
import re
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Optional

BASE_TIME = "2026-06-20T00:45:00+03:00"
TZ = timezone(timedelta(hours=3))
BASE_DT = datetime.fromisoformat(BASE_TIME)

STATUSES = {"success", "partial", "needs_clarification", "ignored", "failed"}
PRIORITIES = {"p1", "p2", "p3", None}
CATEGORIES = {
    "work", "shopping", "home", "health", "finance", "car", "study",
    "family", "garden", "personal", "unknown", None,
}
ASSIGNEES = ["Иван Трифонов", "мама", "Леша", "Наташа", "Сергей", "родители", "тетя Наташа"]

COMMANDS = ["/start", "/week", "/today", "/help", "/done", "/list", "/cancel", "/month", "/settings"]
GARBAGE_IGNORED = ["привет", "ок", "спасибо", "ахаха", "что делаешь", "🔥🔥🔥", "ага", "ну да", "понял", "лол"]
GARBAGE_FAILED = ["ываоывап", "asdfgh", "???", ".....", "кеккек 123 !@#", "ааааа", "жжжжж", "123456789"]

DATE_TIMES = [
    ("сегодня", "2026-06-20", "13:00", "p1"),
    ("сегодня утром", "2026-06-20", "09:00", "p1"),
    ("сегодня днем", "2026-06-20", "13:00", "p1"),
    ("сегодня после обеда", "2026-06-20", "14:00", "p1"),
    ("сегодня вечером", "2026-06-20", "19:00", "p1"),
    ("сегодня ночью", "2026-06-20", "23:00", "p1"),
    ("завтра", "2026-06-21", "13:00", "p2"),
    ("завтра утром", "2026-06-21", "09:00", "p2"),
    ("завтра днем", "2026-06-21", "13:00", "p2"),
    ("завтра после обеда", "2026-06-21", "14:00", "p2"),
    ("завтра вечером", "2026-06-21", "19:00", "p2"),
    ("послезавтра", "2026-06-22", "13:00", "p2"),
    ("послезавтра утром", "2026-06-22", "09:00", "p2"),
    ("в воскресенье", "2026-06-21", "13:00", "p2"),
    ("в воскресенье утром", "2026-06-21", "09:00", "p2"),
    ("в понедельник", "2026-06-22", "13:00", "p2"),
    ("в понедельник утром", "2026-06-22", "09:00", "p2"),
    ("в среду", "2026-06-24", "13:00", "p2"),
    ("до среды", "2026-06-24", "23:59", "p2"),
    ("в пятницу", "2026-06-26", "13:00", "p2"),
    ("до пятницы", "2026-06-26", "23:59", "p2"),
    ("в субботу", "2026-06-20", "13:00", "p1"),
    ("в субботу утром", "2026-06-20", "09:00", "p1"),
    ("до конца недели", "2026-06-21", "23:59", "p2"),
    ("до конца месяца", "2026-06-30", "23:59", "p2"),
    ("1 июля", "2026-07-01", "13:00", "p2"),
    ("через час", "2026-06-20", "01:45", "p1"),
    ("через полчаса", "2026-06-20", "01:15", "p1"),
    ("минут через 20", "2026-06-20", "01:05", "p1"),
    ("через 2 часа", "2026-06-20", "02:45", "p1"),
    ("через 3 дня", "2026-06-23", "13:00", "p2"),
    ("через неделю", "2026-06-27", "13:00", "p2"),
    ("через месяц", "2026-07-20", "13:00", "p2"),
    ("после работы", "2026-06-20", "19:00", "p2"),
    ("перед работой", "2026-06-20", "09:00", "p2"),
    ("к обеду", "2026-06-20", "13:00", "p2"),
    ("на выходных", "2026-06-21", "13:00", "p3"),
    ("на следующей неделе", "2026-06-22", "13:00", "p3"),
    ("на будущей неделе", "2026-06-22", "13:00", "p3"),
]

TASKS = {
    "work": ["написать селф-ревью", "созвон с продактом", "подготовить презентацию для ИК", "сделать отчет по работе", "написать итоги после дейлика", "проверить прод", "отправить документы", "разобрать почту", "проверить метрики", "написать ТДР"],
    "shopping": ["купить молоко", "купить хлеб и молоко", "купить пиво", "купить корм котам", "купить корм собаке", "заказать SSD", "купить продукты", "купить подарок Наташе", "купить лекарства", "заказать фильтр"],
    "home": ["вынести мусор", "разобрать гараж", "почистить фильтр", "помыть окна", "прибраться", "разобрать балкон", "проверить духовку", "починить розетку", "разобрать старые файлы", "помыть пол"],
    "health": ["записаться к стоматологу", "принимать витамин D", "сдать анализы", "позвонить врачу", "выпить магний", "купить таблетки", "записаться к врачу", "проверить давление", "сходить на тренировку", "купить омегу"],
    "finance": ["оплатить ипотеку", "пополнить брокерский счет", "оплатить налог", "проверить вклады", "платить за интернет", "оплатить интернет", "посчитать бюджет", "проверить облигации", "пополнить накопительный счет", "заплатить коммуналку"],
    "car": ["поменять резину", "проверить масло", "записаться на ТО", "купить канистру для бензина", "помыть машину", "купить масло для машины", "проверить страховку", "заехать на мойку", "проверить давление в шинах", "заменить дворники"],
    "study": ["прочитать лекцию", "сделать домашку", "подготовиться к экзамену", "написать главу диплома", "разобрать PID-регуляторы", "посмотреть вебинар", "сдать отчет по практике", "повторить PostgreSQL", "решить задачу по алгоритмам", "прочитать статью"],
    "family": ["позвонить маме", "встретить тетю Наташу в Домодедово", "помочь маме с грядками", "купить подарок Наташе", "отвезти родителей на дачу", "позвонить родителям", "забрать посылку для мамы", "попросить Лешу проверить документы", "поздравить Сергея", "заехать к маме"],
    "garden": ["полить огурцы", "подкормить петунии", "проверить эустомы", "обработать смородину", "проветривать теплицу", "поливать рассаду", "подвязать томаты", "прополоть грядки", "посадить укроп", "проверить клубнику"],
    "personal": ["проверить задачи", "планировать неделю", "перебрать фотографии", "почитать книгу", "сходить погулять", "разобрать заметки", "проверить календарь", "написать план дня", "купить билеты", "позвонить другу"],
}

REPEATS = [
    ("каждый день в 9 утра", "RRULE:FREQ=DAILY;BYHOUR=9;BYMINUTE=0"),
    ("каждое утро", "RRULE:FREQ=DAILY;BYHOUR=9;BYMINUTE=0"),
    ("каждый понедельник", "RRULE:FREQ=WEEKLY;BYDAY=MO"),
    ("раз в неделю", "RRULE:FREQ=WEEKLY"),
    ("каждые 2 дня", "RRULE:FREQ=DAILY;INTERVAL=2"),
    ("каждые 3 дня", "RRULE:FREQ=DAILY;INTERVAL=3"),
    ("по будням в 10", "RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=10;BYMINUTE=0"),
    ("каждый вторник и четверг", "RRULE:FREQ=WEEKLY;BYDAY=TU,TH"),
    ("раз в две недели", "RRULE:FREQ=WEEKLY;INTERVAL=2"),
    ("каждое первое число", "RRULE:FREQ=MONTHLY;BYMONTHDAY=1"),
]

PREFIXES = ["", "надо ", "нужно ", "не забыть ", "закинь в задачи ", "напомни ", "блин не забыть бы ", "плиз ", "пж ", "крч ", "важно "]
TYPO_MAP = str.maketrans({"о": "а", "е": "и", "и": "е", "т": "т", "в": "ф"})

@dataclass(frozen=True)
class Example:
    input: str
    output: dict[str, Any]


def iso(date: str, time: str) -> str:
    return f"{date}T{time}:00+03:00"


def minus(dt_iso: Optional[str], **kwargs: int) -> Optional[str]:
    if dt_iso is None:
        return None
    return (datetime.fromisoformat(dt_iso) - timedelta(**kwargs)).isoformat()


def null_output(status: str) -> dict[str, Any]:
    return {
        "title": None,
        "due_at": None,
        "remind_at": None,
        "priority": None,
        "category": None,
        "assignee": None,
        "repeat": None,
        "status": status,
        "clarification_reason": None,
    }


def make_output(title: Optional[str], due_at: Optional[str], priority: Optional[str], category: Optional[str],
                assignee: Optional[str] = "Иван Трифонов", repeat: Optional[str] = None,
                status: str = "success", clarification_reason: Optional[str] = None,
                remind_at: Optional[str] = "AUTO") -> dict[str, Any]:
    if remind_at == "AUTO":
        remind_at = minus(due_at, hours=1) if due_at else None
    return {
        "title": title,
        "due_at": due_at,
        "remind_at": remind_at,
        "priority": priority,
        "category": category,
        "assignee": assignee,
        "repeat": repeat,
        "status": status,
        "clarification_reason": clarification_reason,
    }


def mutate_text(rng: random.Random, text: str) -> str:
    variants = [text]
    variants.append(text.replace("завтра", "завтро").replace("сегодня", "севодня").replace("молоко", "малако").replace("воскресенье", "воскрисенье"))
    variants.append(text.replace("завтра", "завтр").replace("сегодня", "седня").replace("интернет", "инет").replace("ипотеку", "ипатеку"))
    variants.append(text.replace("пожалуйста", "пжлст").replace("короче", "крч").replace("вечером", "вечерком"))
    variants.append(text.replace("заплатить", "заплотить").replace("стоматологу", "стомотологу").replace("молоко", "молокл"))
    variants.append(text.replace("сегодня", "cегодня"))  # latin c in a Cyrillic word.
    variants.append(text.replace("ё", "е").replace("й", "и"))
    variants.append(text.capitalize())
    variants.append(text.upper() if rng.random() < 0.08 else text)
    variants.append(text + rng.choice(["", " пожалуйста", " плз", " !!!", " ок?", " только не забыть"]))
    words = text.split()
    if len(words) > 3:
        i = rng.randrange(len(words)-1)
        words[i], words[i+1] = words[i+1], words[i]
        variants.append(" ".join(words))
    
    # add harmless live-speech noise to greatly increase uniqueness
    if rng.random() < 0.45:
        variants.append(rng.choice(["слушай ", "короче ", "крч ", "так ", "бот ", ""]) + text + rng.choice(["", " пожалуйста", " не забыть", " пж", " ок", " срочно", " когда сможешь"]))
    if rng.random() < 0.25:
        variants.append(text + " #" + str(rng.randint(1000, 999999)))
    if rng.random() < 0.10 and "купить молоко" in text:
        variants.append(text.replace("купить молоко", "купитьмолоко"))
    return rng.choice(variants).strip()


def build_success(rng: random.Random) -> Example:
    cat = rng.choice(list(TASKS.keys()))
    task = rng.choice(TASKS[cat])
    phrase, date, tm, base_prio = rng.choice(DATE_TIMES)
    pfx = rng.choice(PREFIXES)
    if rng.random() < 0.17:
        # explicit relative reminder
        remind_phrase, delta = rng.choice([("напомни за 15 минут", timedelta(minutes=15)), ("напомни за час", timedelta(hours=1)), ("напомни за день", timedelta(days=1))])
        inp = f"{pfx}{phrase} {task}, {remind_phrase}"
        due = iso(date, tm)
        rem = (datetime.fromisoformat(due) - delta).isoformat()
    else:
        inp = rng.choice([f"{pfx}{phrase} {task}", f"{pfx}{task} {phrase}", f"{phrase} надо {task}"])
        due = iso(date, tm)
        rem = "AUTO"
    prio = "p1" if ("срочно" in inp or "важно" in inp or base_prio == "p1") else base_prio
    if rng.random() < 0.08:
        inp = "p1 " + inp
        prio = "p1"
    if rng.random() < 0.08:
        inp = "не срочно " + inp
        prio = "p3"
    assignee = "Иван Трифонов"
    if rng.random() < 0.12:
        assignee = rng.choice(ASSIGNEES[1:])
        inp = f"{assignee} {inp}"
    inp = mutate_text(rng, inp)
    return Example(inp, make_output(task, due, prio, cat, assignee=assignee, remind_at=rem))


def build_repeat_success(rng: random.Random) -> Example:
    cat = rng.choice(["health", "finance", "garden", "personal", "work", "home"])
    task = rng.choice(TASKS[cat])
    phrase, rule = rng.choice(REPEATS)
    inp = mutate_text(rng, f"{phrase} {task}")
    # Due for first occurrence is intentionally nullable for recurring intent; model stores repeat rule.
    return Example(inp, make_output(task, None, "p2" if cat != "home" else "p3", cat, repeat=rule))


def build_partial(rng: random.Random) -> Example:
    cat = rng.choice(list(TASKS.keys()))
    task = rng.choice(TASKS[cat])
    mode = rng.choice(["no_time", "no_category", "no_assignee", "vague_later"])
    if mode == "no_time":
        inp = rng.choice([f"надо {task}", f"не забыть {task}", f"закинь в задачи {task}", f"потом {task}"])
        out = make_output(task, None, "p3", cat, status="partial", clarification_reason="missing_due_at")
    elif mode == "no_category":
        phrase, date, tm, pr = rng.choice(DATE_TIMES)
        inp = f"{phrase} {task}"
        out = make_output(task, iso(date, tm), pr, "unknown", status="partial", clarification_reason="category_uncertain")
    elif mode == "no_assignee":
        phrase, date, tm, pr = rng.choice(DATE_TIMES)
        inp = f"кому-то {phrase} {task}"
        out = make_output(task, iso(date, tm), pr, cat, assignee=None, status="partial", clarification_reason="assignee_missing")
    else:
        inp = rng.choice([f"когда будет время {task}", f"как-нибудь {task}", f"потом бы {task}"])
        out = make_output(task, None, "p3", cat, status="partial", clarification_reason="vague_due_at")
    return Example(mutate_text(rng, inp), out)


def build_clarification(rng: random.Random) -> Example:
    options = [
        ("позвонить маме утром", "позвонить маме", "family", "ambiguous_date"),
        ("потом купить подарок", "купить подарок", "shopping", "ambiguous_due_at"),
        ("завтра вчера отправить отчет", "отправить отчет", "work", "conflicting_dates"),
        ("в понедельник во вторник созвон", "созвон", "work", "conflicting_dates"),
        ("встретить Наташу в аэропорту утром", "встретить Наташу в аэропорту", "family", "missing_date"),
        ("сделать это завтра", "сделать это", "unknown", "unclear_title"),
        ("купить штуку для машины", "купить штуку для машины", "car", "unclear_item"),
        ("перенести встречу", "перенести встречу", "work", "missing_target_event"),
        ("перенеси созвон на пятницу", "перенести созвон", "work", "missing_target_event"),
        ("отмени молоко", "отменить молоко", "shopping", "missing_target_event"),
        ("сдвинь встречу на час", "сдвинуть встречу", "work", "missing_target_event"),
        ("удали напоминание про врача", "удалить напоминание про врача", "health", "missing_target_event"),
        ("завтра купить молоко и вынести мусор", "купить молоко и вынести мусор", "unknown", "multiple_tasks"),
        ("сегодня оплатить интернет, записаться к врачу", "оплатить интернет, записаться к врачу", "unknown", "multiple_tasks"),
    ]
    inp, title, cat, reason = rng.choice(options)
    out = make_output(title, None, None, cat, status="needs_clarification", clarification_reason=reason)
    return Example(mutate_text(rng, inp), out)


def build_ignored_failed(rng: random.Random) -> Example:
    if rng.random() < 0.55:
        text = rng.choice(COMMANDS + GARBAGE_IGNORED)
        return Example(text, null_output("ignored"))
    text = rng.choice(GARBAGE_FAILED)
    return Example(text, null_output("failed"))


def hardcoded_examples() -> list[Example]:
    examples = []
    seeds = [
        ("Завтра купить молоко", "купить молоко", "2026-06-21", "13:00", "p2", "shopping"),
        ("Сегодня вечером вынести мусор", "вынести мусор", "2026-06-20", "19:00", "p1", "home"),
        ("Послезавтра оплатить интернет", "оплатить интернет", "2026-06-22", "13:00", "p2", "finance"),
        ("В пятницу отправить документы", "отправить документы", "2026-06-26", "13:00", "p2", "work"),
        ("До конца недели написать отчет", "сделать отчет по работе", "2026-06-21", "23:59", "p2", "work"),
        ("Завтра утром написать селф-ревью", "написать селф-ревью", "2026-06-21", "09:00", "p2", "work"),
        ("Сегодня в 16:00 созвон с продактом", "созвон с продактом", "2026-06-20", "16:00", "p1", "work"),
        ("1 июля оплатить ипотеку", "оплатить ипотеку", "2026-07-01", "13:00", "p2", "finance"),
        ("01.07 оплатить налог", "оплатить налог", "2026-07-01", "13:00", "p2", "finance"),
        ("1-го июля оплатить налог", "оплатить налог", "2026-07-01", "13:00", "p2", "finance"),
        ("В воскресенье встретить тетю Наташу в Домодедово в 10 утра", "встретить тетю Наташу в Домодедово", "2026-06-21", "10:00", "p2", "family"),
        ("Седня к 16-30 оплатить инет", "оплатить интернет", "2026-06-20", "16:30", "p1", "finance"),
        ("Крч завтра в 7 купить молоко", "купить молоко", "2026-06-21", "07:00", "p2", "shopping"),
        ("Минут через 20 проверить духовку", "проверить духовку", "2026-06-20", "01:05", "p1", "home"),
    ]
    for inp, title, d, t, p, c in seeds:
        examples.append(Example(inp, make_output(title, iso(d, t), p, c)))
    examples.extend([
        Example("Каждый день в 9 утра проверить задачи", make_output("проверить задачи", None, "p2", "personal", repeat="RRULE:FREQ=DAILY;BYHOUR=9;BYMINUTE=0")),
        Example("Каждые 2 дня проветривать теплицу", make_output("проветривать теплицу", None, "p2", "garden", repeat="RRULE:FREQ=DAILY;INTERVAL=2")),
        Example("Раз в две недели проверять вклады", make_output("проверить вклады", None, "p2", "finance", repeat="RRULE:FREQ=WEEKLY;INTERVAL=2")),
        Example("Каждый вторник и четверг сходить на тренировку", make_output("сходить на тренировку", None, "p2", "health", repeat="RRULE:FREQ=WEEKLY;BYDAY=TU,TH")),
        Example("/start", null_output("ignored")),
        Example("/week", null_output("ignored")),
        Example("привет", null_output("ignored")),
        Example("ываоывап", null_output("failed")),
        Example("Завтра вчера отправить отчет", make_output("отправить отчет", None, None, "work", status="needs_clarification", clarification_reason="conflicting_dates")),
    ])
    return examples


def build_dataset(n: int, seed: int, reserved_inputs: set[str] | None = None) -> list[dict[str, Any]]:
    rng = random.Random(seed)
    reserved_inputs = reserved_inputs or set()
    examples: list[Example] = []
    if not reserved_inputs:
        examples.extend(hardcoded_examples())
    target = {
        "success": int(n * 0.45),
        "partial": int(n * 0.25),
        "needs_clarification": int(n * 0.15),
    }
    target["ignored_failed"] = n - sum(target.values())
    builders = ([build_success, build_repeat_success], [build_partial], [build_clarification], [build_ignored_failed])
    seen = {e.input for e in examples} | reserved_inputs
    def add_unique(e: Example) -> bool:
        if not e.input or e.input in seen:
            return False
        seen.add(e.input)
        examples.append(e)
        return True
    counts = {"success": sum(1 for e in examples if e.output["status"] == "success"),
              "partial": sum(1 for e in examples if e.output["status"] == "partial"),
              "needs_clarification": sum(1 for e in examples if e.output["status"] == "needs_clarification"),
              "ignored_failed": sum(1 for e in examples if e.output["status"] in {"ignored", "failed"})}
    while counts["success"] < target["success"]:
        e = rng.choice(builders[0])(rng)
        if add_unique(e): counts["success"] += 1
    while counts["partial"] < target["partial"]:
        e = build_partial(rng)
        if add_unique(e): counts["partial"] += 1
    while counts["needs_clarification"] < target["needs_clarification"]:
        e = build_clarification(rng)
        if add_unique(e): counts["needs_clarification"] += 1
    while counts["ignored_failed"] < target["ignored_failed"]:
        # create more unique no-task/failed variants by appending harmless noise
        e = build_ignored_failed(rng)
        if e.input in seen:
            e = Example(e.input + " " + str(rng.randint(1, 999999)), e.output)
        if add_unique(e): counts["ignored_failed"] += 1
    rng.shuffle(examples)
    return [{"input": e.input, "base_time": BASE_TIME, "output": e.output} for e in examples[:n]]


def validate_file(path: Path) -> dict[str, int]:
    counts: dict[str, int] = {s: 0 for s in STATUSES}
    seen_inputs: set[str] = set()
    for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        try:
            obj = json.loads(line)
        except json.JSONDecodeError as exc:
            raise AssertionError(f"{path}:{lineno}: invalid JSON: {exc}")
        assert obj.get("input"), f"{path}:{lineno}: empty input"
        assert obj.get("base_time") == BASE_TIME, f"{path}:{lineno}: invalid base_time"
        out = obj.get("output")
        assert isinstance(out, dict), f"{path}:{lineno}: output must be object"
        status = out.get("status")
        assert status in STATUSES, f"{path}:{lineno}: invalid status {status}"
        counts[status] += 1
        assert out.get("priority") in PRIORITIES, f"{path}:{lineno}: invalid priority"
        assert out.get("category") in CATEGORIES, f"{path}:{lineno}: invalid category"
        for key in ["due_at", "remind_at"]:
            val = out.get(key)
            if val is not None:
                datetime.fromisoformat(val)
        due = out.get("due_at")
        rem = out.get("remind_at")
        if due is not None and rem is not None:
            assert datetime.fromisoformat(rem) <= datetime.fromisoformat(due), f"{path}:{lineno}: remind_at after due_at"
        if status in {"ignored", "failed"}:
            for key in ["title", "due_at", "remind_at", "priority", "category", "assignee", "repeat", "clarification_reason"]:
                assert out.get(key) is None, f"{path}:{lineno}: {status} must have null {key}"
        seen_inputs.add(obj["input"])
    return counts


def write_jsonl(path: Path, rows: list[dict[str, Any]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as f:
        for row in rows:
            f.write(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", default=".", help="Project root output directory")
    parser.add_argument("--seed", type=int, default=424242)
    parser.add_argument("--train", type=int, default=5000)
    parser.add_argument("--valid", type=int, default=500)
    args = parser.parse_args()

    root = Path(args.out)
    ds = root / "datasets" / "planning_ru"
    train = build_dataset(args.train, args.seed)
    valid = build_dataset(args.valid, args.seed + 1, reserved_inputs={r["input"] for r in train})
    write_jsonl(ds / "train.jsonl", train)
    write_jsonl(ds / "valid.jsonl", valid)

    train_counts = validate_file(ds / "train.jsonl")
    valid_counts = validate_file(ds / "valid.jsonl")
    train_inputs = {json.loads(x)["input"] for x in (ds / "train.jsonl").read_text(encoding="utf-8").splitlines()}
    valid_inputs = {json.loads(x)["input"] for x in (ds / "valid.jsonl").read_text(encoding="utf-8").splitlines()}
    assert not (train_inputs & valid_inputs), "train/valid full input duplicates found"
    print(json.dumps({"train": train_counts, "valid": valid_counts}, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
