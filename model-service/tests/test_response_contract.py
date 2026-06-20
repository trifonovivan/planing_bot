from __future__ import annotations

import unittest

from message_parser.schemas import ParseResponse, ParserOutput


class ResponseContractTest(unittest.TestCase):
    def test_parse_response_contains_version_metadata(self) -> None:
        response = ParseResponse(
            output=ParserOutput(title="купить молоко", status="success"),
            confidence=0.9,
            field_confidence={"title": 0.95},
            source="hybrid",
            time_source="date_word",
            model_version="test-model-v1",
        )

        payload = response.model_dump()
        self.assertEqual("test-model-v1", payload["model_version"])
        self.assertEqual("0.1.0", payload["parser_version"])
        self.assertEqual({"title": 0.95}, payload["field_confidence"])


if __name__ == "__main__":
    unittest.main()
