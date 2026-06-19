from __future__ import annotations

import argparse
import json

from message_parser.model import load_model


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True)
    parser.add_argument("--text", required=True)
    parser.add_argument("--base-time")
    args = parser.parse_args()

    model = load_model(args.model)
    response = model.predict(args.text, args.base_time)
    print(json.dumps(response.model_dump(), ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
