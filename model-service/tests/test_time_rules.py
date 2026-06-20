from __future__ import annotations

import unittest
from datetime import datetime, timezone, timedelta

from message_parser.time_rules import resolve_time


class TimeRulesTest(unittest.TestCase):
    def setUp(self) -> None:
        self.base = datetime(2026, 6, 20, 0, 45, tzinfo=timezone(timedelta(hours=3)))

    def test_tomorrow_evening(self) -> None:
        result = resolve_time("завтра вечером купить молоко", self.base)
        self.assertEqual("2026-06-21T19:00:00+03:00", result.due_at.isoformat())
        self.assertEqual("2026-06-21T18:00:00+03:00", result.remind_at.isoformat())

    def test_relative_hours(self) -> None:
        result = resolve_time("через 2 часа проверить задачу", self.base)
        self.assertEqual("2026-06-20T02:45:00+03:00", result.due_at.isoformat())

    def test_explicit_reminder(self) -> None:
        result = resolve_time("перед работой посмотреть вебинар, напомни за день", self.base)
        self.assertEqual("2026-06-20T09:00:00+03:00", result.due_at.isoformat())
        self.assertEqual("2026-06-19T09:00:00+03:00", result.remind_at.isoformat())

    def test_slang_and_dash_time(self) -> None:
        result = resolve_time("седня к 16-30 оплатить инет", self.base)
        self.assertEqual("2026-06-20T16:30:00+03:00", result.due_at.isoformat())

    def test_minutes_later(self) -> None:
        result = resolve_time("минут через 20 проверить духовку", self.base)
        self.assertEqual("2026-06-20T01:05:00+03:00", result.due_at.isoformat())

    def test_bare_morning_uses_next_occurrence(self) -> None:
        result = resolve_time("полить огурцы утром", self.base)
        self.assertEqual("2026-06-20T09:00:00+03:00", result.due_at.isoformat())
        self.assertEqual("2026-06-20T08:00:00+03:00", result.remind_at.isoformat())

    def test_bare_evening_uses_next_occurrence(self) -> None:
        result = resolve_time("побрызгать огурцы вечером", self.base)
        self.assertEqual("2026-06-20T19:00:00+03:00", result.due_at.isoformat())
        self.assertEqual("2026-06-20T18:00:00+03:00", result.remind_at.isoformat())

    def test_end_of_month_variants(self) -> None:
        result = resolve_time("в конце месяца оплатить инет", self.base)
        self.assertEqual("2026-06-30T23:59:00+03:00", result.due_at.isoformat())
        self.assertEqual("2026-06-30T22:59:00+03:00", result.remind_at.isoformat())

        result = resolve_time("к концу месяца оплатить интернет", self.base)
        self.assertEqual("2026-06-30T23:59:00+03:00", result.due_at.isoformat())

    def test_correction_prefers_phrase_after_but(self) -> None:
        result = resolve_time("не завтра, а в понедельник полить огурцы", self.base)
        self.assertEqual("2026-06-22T13:00:00+03:00", result.due_at.isoformat())

    def test_spaced_after_tomorrow(self) -> None:
        result = resolve_time("после завтра выключить духовку", self.base)
        self.assertEqual("2026-06-22T13:00:00+03:00", result.due_at.isoformat())

    def test_weekday_with_na_preposition(self) -> None:
        result = resolve_time("созвон на четверг", self.base)
        self.assertEqual("2026-06-25T13:00:00+03:00", result.due_at.isoformat())

    def test_clock_without_date_uses_next_occurrence(self) -> None:
        result = resolve_time("к 8 вечера полить огурцы", self.base)
        self.assertEqual("2026-06-20T20:00:00+03:00", result.due_at.isoformat())

    def test_relative_word_number_weeks(self) -> None:
        result = resolve_time("через две недели проверить алертинг", self.base)
        self.assertEqual("2026-07-04T13:00:00+03:00", result.due_at.isoformat())


if __name__ == "__main__":
    unittest.main()
