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
import sys
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Optional

SRC_ROOT = Path(__file__).resolve().parents[1] / "src"
if str(SRC_ROOT) not in sys.path:
    sys.path.insert(0, str(SRC_ROOT))

from message_parser.normalizer import normalize_text
from message_parser.time_rules import resolve_time

BASE_TIME = "2026-06-20T00:45:00+03:00"
TZ = timezone(timedelta(hours=3))
BASE_DT = datetime.fromisoformat(BASE_TIME)
TRAIN_BASE_TIMES = [
    BASE_TIME,
    "2026-06-24T08:10:00+03:00",
    "2026-07-03T18:35:00+03:00",
]
VALID_BASE_TIMES = [
    "2026-07-09T11:20:00+03:00",
    "2026-08-17T21:05:00+03:00",
    "2026-11-02T07:50:00+03:00",
]

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
    ("утром", "2026-06-20", "09:00", "p1"),
    ("днем", "2026-06-20", "13:00", "p1"),
    ("после обеда", "2026-06-20", "14:00", "p1"),
    ("вечером", "2026-06-20", "19:00", "p1"),
    ("ночью", "2026-06-20", "23:00", "p1"),
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
    ("в конце месяца", "2026-06-30", "23:59", "p2"),
    ("к концу месяца", "2026-06-30", "23:59", "p2"),
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

VALID_DATE_TIMES = [
    ("на сегодня в 20:15", "", "", "p1"),
    ("к 8 вечера", "", "", "p1"),
    ("на четверг", "", "", "p2"),
    ("после завтра", "", "", "p2"),
    ("через 90 минут", "", "", "p1"),
    ("через две недели", "", "", "p3"),
    ("не завтра, а в понедельник", "", "", "p2"),
    ("до следующей среды", "", "", "p2"),
    ("на будущей неделе", "", "", "p3"),
]

TASKS = {
    "work": ["написать селф-ревью", "дописать селф-ревью", "созвон с продактом", "подготовить презентацию для ИК", "сделать отчет по работе", "написать итоги после дейлика", "проверить прод", "отправить документы", "разобрать почту", "проверить метрики", "написать ТДР"],
    "shopping": ["купить молоко", "купить хлеб и молоко", "купить пиво", "купить корм котам", "купить корм собаке", "заказать SSD", "купить продукты", "купить подарок Наташе", "купить лекарства", "заказать фильтр"],
    "home": ["вынести мусор", "разобрать гараж", "почистить фильтр", "помыть окна", "прибраться", "разобрать балкон", "проверить духовку", "починить розетку", "разобрать старые файлы", "помыть пол"],
    "health": ["записаться к стоматологу", "принимать витамин D", "сдать анализы", "позвонить врачу", "выпить магний", "купить таблетки", "записаться к врачу", "проверить давление", "сходить на тренировку", "купить омегу"],
    "finance": ["оплатить ипотеку", "пополнить брокерский счет", "оплатить налог", "проверить вклады", "платить за интернет", "оплатить интернет", "посчитать бюджет", "проверить облигации", "пополнить накопительный счет", "заплатить коммуналку"],
    "car": ["поменять резину", "проверить масло", "записаться на ТО", "купить канистру для бензина", "помыть машину", "купить масло для машины", "проверить страховку", "заехать на мойку", "проверить давление в шинах", "заменить дворники"],
    "study": ["прочитать лекцию", "сделать домашку", "подготовиться к экзамену", "написать главу диплома", "разобрать PID-регуляторы", "посмотреть вебинар", "сдать отчет по практике", "повторить PostgreSQL", "решить задачу по алгоритмам", "прочитать статью"],
    "family": ["позвонить маме", "встретить тетю Наташу в Домодедово", "помочь маме с грядками", "отвезти родителей на дачу", "позвонить родителям", "забрать посылку для мамы", "попросить Лешу проверить документы", "поздравить Сергея", "заехать к маме"],
    "garden": ["полить огурцы", "полить петунии", "поливать петунии", "подкормить петунии", "побрызгать огурцы", "опрыскать огурцы", "проверить эустомы", "обработать смородину", "проветривать теплицу", "поливать рассаду", "подвязать томаты", "прополоть грядки", "посадить укроп", "проверить клубнику"],
    "personal": ["проверить задачи", "планировать неделю", "перебрать фотографии", "почитать книгу", "сходить погулять", "разобрать заметки", "проверить календарь", "написать план дня", "купить билеты", "позвонить другу"],
}

VALID_TASKS = {
    "work": ["согласовать роадмап", "проверить алертинг", "дописать RFC", "обновить план релиза"],
    "shopping": ["заказать продукты", "купить зарядку", "выбрать рюкзак", "докупить кофе"],
    "home": ["выключить духовку", "проверить стиралку", "заменить лампочку", "разморозить холодильник"],
    "health": ["забронировать чек-ап", "поставить прививку", "купить пластырь", "записаться на массаж"],
    "finance": ["оплатить парковку", "проверить кешбэк", "закрыть кредитку", "сверить расходы"],
    "car": ["заправить машину", "проверить аккумулятор", "забрать машину из сервиса", "купить щетки"],
    "study": ["посмотреть курс по ml", "дочитать конспект", "подготовить доклад", "разобрать домашку по sql"],
    "family": ["позвонить бабушке", "забрать диму из школы", "поздравить олю", "написать сестре"],
    "garden": ["полить кабачки", "подкормить розы", "накрыть клубнику", "проверить рассаду перцев"],
    "personal": ["обновить резюме", "записать идею", "почистить рабочий стол", "забронировать билеты"],
}

UNKNOWN_CATEGORY_TASKS = [
    "разобрать вопрос",
    "уточнить детали",
    "подготовить список",
    "посмотреть это",
    "проверить штуку",
    "сделать одно дело",
    "доделать начатое",
    "вернуться к теме",
]

VALID_UNKNOWN_CATEGORY_TASKS = [
    "разобрать непонятное",
    "уточнить вводные",
    "собрать черновик",
    "посмотреть ссылку",
    "проверить гипотезу",
    "сделать важное",
    "закрыть хвост",
    "вернуться к обсуждению",
]

CLARIFICATION_OPTIONS = [
    ("позвонить маме когда проснусь", "позвонить маме", "family", "ambiguous_date"),
    ("потом купить подарок", "купить подарок", "shopping", "ambiguous_due_at"),
    ("завтра вчера отправить отчет", "отправить отчет", "work", "conflicting_dates"),
    ("в понедельник во вторник созвон", "созвон", "work", "conflicting_dates"),
    ("встретить Наташу в аэропорту на днях", "встретить Наташу в аэропорту", "family", "missing_date"),
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

VALID_CLARIFICATION_OPTIONS = [
    ("на днях позвонить бабушке", "позвонить бабушке", "family", "missing_date"),
    ("потом заказать что-нибудь", "заказать что-нибудь", "shopping", "ambiguous_due_at"),
    ("сегодня вчера проверить алертинг", "проверить алертинг", "work", "conflicting_dates"),
    ("в среду в пятницу доклад", "подготовить доклад", "study", "conflicting_dates"),
    ("забрать Диму когда получится", "забрать диму", "family", "ambiguous_date"),
    ("сделать штуку вечером", "сделать штуку", "unknown", "unclear_title"),
    ("купить деталь для машины", "купить деталь для машины", "car", "unclear_item"),
    ("перенести чек-ап", "перенести чек-ап", "health", "missing_target_event"),
    ("перенеси релиз на четверг", "перенести релиз", "work", "missing_target_event"),
    ("отмени доставку кофе", "отменить доставку кофе", "shopping", "missing_target_event"),
    ("сдвинь доклад на час", "сдвинуть доклад", "study", "missing_target_event"),
    ("удали напоминание про парковку", "удалить напоминание про парковку", "finance", "missing_target_event"),
    ("завтра заправить машину и купить зарядку", "заправить машину и купить зарядку", "unknown", "multiple_tasks"),
    ("сегодня поставить прививку, сверить расходы", "поставить прививку, сверить расходы", "unknown", "multiple_tasks"),
]

REPEATS = [
    ("каждый день", "RRULE:FREQ=DAILY"),
    ("ежедневно", "RRULE:FREQ=DAILY"),
    ("каждый день утром", "RRULE:FREQ=DAILY;BYHOUR=9;BYMINUTE=0"),
    ("каждый день вечером", "RRULE:FREQ=DAILY;BYHOUR=19;BYMINUTE=0"),
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

VALID_REPEATS = [
    ("каждый месяц", "RRULE:FREQ=MONTHLY"),
    ("ежемесячно", "RRULE:FREQ=MONTHLY"),
    ("раз в месяц", "RRULE:FREQ=MONTHLY"),
    ("каждый день вечером", "RRULE:FREQ=DAILY;BYHOUR=19;BYMINUTE=0"),
    ("по будням", "RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"),
]

PREFIXES = ["", "надо ", "нужно ", "не забыть ", "закинь в задачи ", "напомни ", "блин не забыть бы ", "плиз ", "пж ", "крч ", "важно "]
TYPO_MAP = str.maketrans({"о": "а", "е": "и", "и": "е", "т": "т", "в": "ф"})

@dataclass(frozen=True)
class Example:
    input: str
    output: dict[str, Any]
    base_time: str = BASE_TIME


@dataclass(frozen=True)
class DatasetProfile:
    base_times: list[str]
    tasks: dict[str, list[str]]
    date_times: list[tuple[str, str, str, str]]
    repeats: list[tuple[str, str]]
    assignees: list[str]
    unknown_category_tasks: list[str]
    clarification_options: list[tuple[str, str, str, str]]
    include_hardcoded: bool = False


def iso(date: str, time: str) -> str:
    return f"{date}T{time}:00+03:00"


def due_for_phrase(phrase: str, base_time: str) -> str:
    base_dt = datetime.fromisoformat(base_time)
    result = resolve_time(phrase, base_dt)
    if result.due_at is None:
        raise AssertionError(f"time phrase did not resolve: {phrase!r} from {base_time}")
    return result.due_at.isoformat()


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
    variants.append(text.replace("каждый", "кадлый").replace("огурцы", "огузцы").replace("побрызгать", "побрыскать"))
    variants.append(text.replace("написать", "напсать").replace("селф-ревью", "селф ревью").replace("огурцы", "огурци"))
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


def build_success(rng: random.Random, profile: DatasetProfile) -> Example:
    base_time = rng.choice(profile.base_times)
    cat = rng.choice(list(profile.tasks.keys()))
    task = rng.choice(profile.tasks[cat])
    phrase, _date, _tm, base_prio = rng.choice(profile.date_times)
    pfx = rng.choice(PREFIXES)
    due = due_for_phrase(phrase, base_time)
    if rng.random() < 0.17:
        # explicit relative reminder
        remind_phrase, delta = rng.choice([("напомни за 15 минут", timedelta(minutes=15)), ("напомни за час", timedelta(hours=1)), ("напомни за день", timedelta(days=1))])
        inp = f"{pfx}{phrase} {task}, {remind_phrase}"
        rem = (datetime.fromisoformat(due) - delta).isoformat()
    else:
        inp = rng.choice([f"{pfx}{phrase} {task}", f"{pfx}{task} {phrase}", f"{phrase} надо {task}"])
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
        assignee = rng.choice(profile.assignees[1:])
        inp = f"{assignee} {inp}"
    inp = mutate_text(rng, inp)
    return Example(inp, make_output(task, due, prio, cat, assignee=assignee, remind_at=rem), base_time)


def build_repeat_success(rng: random.Random, profile: DatasetProfile) -> Example:
    base_time = rng.choice(profile.base_times)
    repeat_categories = [cat for cat in ["health", "finance", "garden", "personal", "work", "home"] if cat in profile.tasks]
    cat = rng.choice(repeat_categories)
    task = rng.choice(profile.tasks[cat])
    phrase, rule = rng.choice(profile.repeats)
    inp = mutate_text(rng, f"{phrase} {task}")
    # Due for first occurrence is intentionally nullable for recurring intent; model stores repeat rule.
    return Example(inp, make_output(task, None, "p2" if cat != "home" else "p3", cat, repeat=rule), base_time)


def build_partial(rng: random.Random, profile: DatasetProfile) -> Example:
    base_time = rng.choice(profile.base_times)
    cat = rng.choice(list(profile.tasks.keys()))
    task = rng.choice(profile.tasks[cat])
    mode = rng.choice(["no_time", "no_category", "no_assignee", "vague_later"])
    if mode == "no_time":
        inp = rng.choice([f"надо {task}", f"не забыть {task}", f"закинь в задачи {task}", f"потом {task}"])
        out = make_output(task, None, "p3", cat, status="partial", clarification_reason="missing_due_at")
    elif mode == "no_category":
        task = rng.choice(profile.unknown_category_tasks)
        phrase, _date, _tm, pr = rng.choice(profile.date_times)
        inp = f"{phrase} {task}"
        out = make_output(task, due_for_phrase(phrase, base_time), pr, "unknown", status="partial", clarification_reason="category_uncertain")
    elif mode == "no_assignee":
        phrase, _date, _tm, pr = rng.choice(profile.date_times)
        inp = f"кому-то {phrase} {task}"
        out = make_output(task, due_for_phrase(phrase, base_time), pr, cat, assignee=None, status="partial", clarification_reason="assignee_missing")
    else:
        inp = rng.choice([f"когда будет время {task}", f"как-нибудь {task}", f"потом бы {task}"])
        out = make_output(task, None, "p3", cat, status="partial", clarification_reason="vague_due_at")
    return Example(mutate_text(rng, inp), out, base_time)


def build_clarification(rng: random.Random, profile: DatasetProfile) -> Example:
    base_time = rng.choice(profile.base_times)
    inp, title, cat, reason = rng.choice(profile.clarification_options)
    out = make_output(title, None, None, cat, status="needs_clarification", clarification_reason=reason)
    return Example(mutate_text(rng, inp), out, base_time)


def build_ignored_failed(rng: random.Random, profile: DatasetProfile) -> Example:
    base_time = rng.choice(profile.base_times)
    if rng.random() < 0.55:
        text = rng.choice(COMMANDS + GARBAGE_IGNORED)
        return Example(text, null_output("ignored"), base_time)
    text = rng.choice(GARBAGE_FAILED)
    return Example(text, null_output("failed"), base_time)


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
        ("Полить петунии завтра утром", "полить петунии", "2026-06-21", "09:00", "p2", "garden"),
        ("Полить огурцы утром", "полить огурцы", "2026-06-20", "09:00", "p1", "garden"),
        ("Полить огурцы сегодня утром", "полить огурцы", "2026-06-20", "09:00", "p1", "garden"),
        ("Побрызгать огурцы вечером", "побрызгать огурцы", "2026-06-20", "19:00", "p1", "garden"),
        ("Побрыскать огузцы вечером", "побрызгать огурцы", "2026-06-20", "19:00", "p1", "garden"),
        ("В конце месяца оплатить инет", "оплатить интернет", "2026-06-30", "23:59", "p2", "finance"),
    ]
    for inp, title, d, t, p, c in seeds:
        examples.append(Example(inp, make_output(title, iso(d, t), p, c)))
    examples.extend([
        Example("Каждый день в 9 утра проверить задачи", make_output("проверить задачи", None, "p2", "personal", repeat="RRULE:FREQ=DAILY;BYHOUR=9;BYMINUTE=0")),
        Example("Каждый день поливать петунии", make_output("поливать петунии", None, "p2", "garden", repeat="RRULE:FREQ=DAILY")),
        Example("Каждый день утром поливать петунии", make_output("поливать петунии", None, "p2", "garden", repeat="RRULE:FREQ=DAILY;BYHOUR=9;BYMINUTE=0")),
        Example("Каждые 2 дня проветривать теплицу", make_output("проветривать теплицу", None, "p2", "garden", repeat="RRULE:FREQ=DAILY;INTERVAL=2")),
        Example("Раз в две недели проверять вклады", make_output("проверить вклады", None, "p2", "finance", repeat="RRULE:FREQ=WEEKLY;INTERVAL=2")),
        Example("Каждый вторник и четверг сходить на тренировку", make_output("сходить на тренировку", None, "p2", "health", repeat="RRULE:FREQ=WEEKLY;BYDAY=TU,TH")),
        Example("Написать Леше через полчаса", make_output("написать Леше", iso("2026-06-20", "01:15"), "p1", "family")),
        Example("Срочно напсать селф ревью", make_output("написать селф-ревью", None, "p1", "work", status="partial", clarification_reason="missing_due_at")),
        Example("/start", null_output("ignored")),
        Example("/week", null_output("ignored")),
        Example("привет", null_output("ignored")),
        Example("ываоывап", null_output("failed")),
        Example("Завтра вчера отправить отчет", make_output("отправить отчет", None, None, "work", status="needs_clarification", clarification_reason="conflicting_dates")),
    ])
    return examples


def build_dataset(
    n: int,
    seed: int,
    profile: DatasetProfile,
    reserved_inputs: set[str] | None = None,
) -> list[dict[str, Any]]:
    rng = random.Random(seed)
    reserved_inputs = reserved_inputs or set()
    examples: list[Example] = []
    if profile.include_hardcoded:
        examples.extend(hardcoded_examples())
    target = {
        "success": int(n * 0.45),
        "partial": int(n * 0.25),
        "needs_clarification": int(n * 0.15),
    }
    target["ignored_failed"] = n - sum(target.values())
    seen = {e.input for e in examples} | reserved_inputs
    reserved_normalized = {normalize_text(text) for text in reserved_inputs}
    labels_by_normalized = {
        normalize_text(e.input): json.dumps(e.output, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
        for e in examples
    }
    def add_unique(e: Example) -> bool:
        normalized = normalize_text(e.input)
        if not e.input or e.input in seen or normalized in reserved_normalized:
            return False
        label = json.dumps(e.output, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
        previous = labels_by_normalized.get(normalized)
        if previous is not None and previous != label:
            return False
        seen.add(e.input)
        labels_by_normalized[normalized] = label
        examples.append(e)
        return True

    def add_generated(e: Example) -> bool:
        if add_unique(e):
            return True
        for _ in range(5):
            if normalize_text(e.input) in reserved_normalized:
                return False
            candidate = Example(f"{e.input} #{rng.randint(1000, 9999999)}", e.output, e.base_time)
            if add_unique(candidate):
                return True
        return False
    counts = {"success": sum(1 for e in examples if e.output["status"] == "success"),
              "partial": sum(1 for e in examples if e.output["status"] == "partial"),
              "needs_clarification": sum(1 for e in examples if e.output["status"] == "needs_clarification"),
              "ignored_failed": sum(1 for e in examples if e.output["status"] in {"ignored", "failed"})}
    while counts["success"] < target["success"]:
        builder = rng.choice([build_success, build_repeat_success])
        e = builder(rng, profile)
        if add_generated(e): counts["success"] += 1
    while counts["partial"] < target["partial"]:
        e = build_partial(rng, profile)
        if add_generated(e): counts["partial"] += 1
    while counts["needs_clarification"] < target["needs_clarification"]:
        e = build_clarification(rng, profile)
        if add_generated(e): counts["needs_clarification"] += 1
    while counts["ignored_failed"] < target["ignored_failed"]:
        # create more unique no-task/failed variants by appending harmless noise
        e = build_ignored_failed(rng, profile)
        if e.input in seen:
            e = Example(e.input + " " + str(rng.randint(1, 999999)), e.output, e.base_time)
        if add_generated(e): counts["ignored_failed"] += 1
    rng.shuffle(examples)
    return [{"input": e.input, "base_time": e.base_time, "output": e.output} for e in examples[:n]]


def validate_file(path: Path) -> dict[str, int]:
    counts: dict[str, int] = {s: 0 for s in STATUSES}
    seen_inputs: set[str] = set()
    for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        try:
            obj = json.loads(line)
        except json.JSONDecodeError as exc:
            raise AssertionError(f"{path}:{lineno}: invalid JSON: {exc}")
        assert obj.get("input"), f"{path}:{lineno}: empty input"
        base_time = obj.get("base_time")
        assert isinstance(base_time, str), f"{path}:{lineno}: invalid base_time"
        datetime.fromisoformat(base_time)
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
        repeat = out.get("repeat")
        title = out.get("title")
        if due is not None and rem is not None:
            assert datetime.fromisoformat(rem) <= datetime.fromisoformat(due), f"{path}:{lineno}: remind_at after due_at"
        if rem is not None:
            assert due is not None, f"{path}:{lineno}: remind_at without due_at"
        if repeat is not None:
            assert due is None and rem is None, f"{path}:{lineno}: repeat must not have due/remind"
        if status == "success":
            assert title, f"{path}:{lineno}: success must have title"
            assert due is not None or repeat is not None, f"{path}:{lineno}: success must have due_at or repeat"
        if status in {"ignored", "failed"}:
            for key in ["title", "due_at", "remind_at", "priority", "category", "assignee", "repeat", "clarification_reason"]:
                assert out.get(key) is None, f"{path}:{lineno}: {status} must have null {key}"
        assert obj["input"] not in seen_inputs, f"{path}:{lineno}: duplicate input"
        seen_inputs.add(obj["input"])
    return counts


def validate_normalized_labels(paths: list[Path]) -> None:
    labels_by_normalized: dict[str, str] = {}
    for path in paths:
        for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            obj = json.loads(line)
            key = normalize_text(obj["input"])
            label = json.dumps(obj["output"], ensure_ascii=False, sort_keys=True, separators=(",", ":"))
            previous = labels_by_normalized.get(key)
            if previous is not None and previous != label:
                raise AssertionError(f"{path}:{lineno}: normalized duplicate has different label: {key!r}")
            labels_by_normalized[key] = label


def write_jsonl(path: Path, rows: list[dict[str, Any]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as f:
        for row in rows:
            f.write(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n")


def normalized_inputs(path: Path) -> set[str]:
    return {
        normalize_text(json.loads(line)["input"])
        for line in path.read_text(encoding="utf-8").splitlines()
        if line.strip()
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", default=".", help="Project root output directory")
    parser.add_argument("--seed", type=int, default=424242)
    parser.add_argument("--train", type=int, default=15000)
    parser.add_argument("--valid", type=int, default=2000)
    args = parser.parse_args()

    root = Path(args.out)
    ds = root / "datasets" / "planning_ru"
    train_profile = DatasetProfile(
        base_times=TRAIN_BASE_TIMES,
        tasks=TASKS,
        date_times=DATE_TIMES,
        repeats=REPEATS,
        assignees=ASSIGNEES,
        unknown_category_tasks=UNKNOWN_CATEGORY_TASKS,
        clarification_options=CLARIFICATION_OPTIONS,
        include_hardcoded=True,
    )
    valid_profile = DatasetProfile(
        base_times=VALID_BASE_TIMES,
        tasks=VALID_TASKS,
        date_times=VALID_DATE_TIMES,
        repeats=VALID_REPEATS,
        assignees=["Иван Трифонов", "Оля", "Дима", "сестра", "бабушка"],
        unknown_category_tasks=VALID_UNKNOWN_CATEGORY_TASKS,
        clarification_options=VALID_CLARIFICATION_OPTIONS,
    )
    train = build_dataset(args.train, args.seed, train_profile)
    valid = build_dataset(args.valid, args.seed + 1, valid_profile, reserved_inputs={r["input"] for r in train})
    write_jsonl(ds / "train.jsonl", train)
    write_jsonl(ds / "valid.jsonl", valid)

    train_counts = validate_file(ds / "train.jsonl")
    valid_counts = validate_file(ds / "valid.jsonl")
    train_inputs = {json.loads(x)["input"] for x in (ds / "train.jsonl").read_text(encoding="utf-8").splitlines()}
    valid_inputs = {json.loads(x)["input"] for x in (ds / "valid.jsonl").read_text(encoding="utf-8").splitlines()}
    assert not (train_inputs & valid_inputs), "train/valid full input duplicates found"
    train_normalized = normalized_inputs(ds / "train.jsonl")
    valid_normalized = normalized_inputs(ds / "valid.jsonl")
    assert not (train_normalized & valid_normalized), "train/valid normalized input duplicates found"
    validate_normalized_labels([ds / "train.jsonl", ds / "valid.jsonl"])
    print(json.dumps({"train": train_counts, "valid": valid_counts}, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
