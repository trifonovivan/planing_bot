from __future__ import annotations

import unittest

from message_parser.title_extractor import extract_title


class TitleExtractorTest(unittest.TestCase):
    def test_extracts_unseen_action_over_confident_prediction(self) -> None:
        title = extract_title("через 90 минут выключить духовку", "проверить духовку", 0.99)
        self.assertEqual("выключить духовку", title)

    def test_extracts_purchase_wording(self) -> None:
        title = extract_title("на сегодня в 20:15 заказать продукты", "купить продукты", 0.99)
        self.assertEqual("заказать продукты", title)

    def test_removes_corrected_time_prefix(self) -> None:
        title = extract_title("не завтра, а в понедельник полить огурцы", None, 0.0)
        self.assertEqual("полить огурцы", title)


if __name__ == "__main__":
    unittest.main()
