from __future__ import annotations

import os
from functools import lru_cache

from fastapi import FastAPI, HTTPException

from message_parser.model import load_model
from message_parser.schemas import ParseRequest, ParseResponse


app = FastAPI(title="Planner Message Parser", version="0.1.0")


@lru_cache(maxsize=1)
def get_model():
    model_path = os.getenv("MODEL_PATH", "artifacts/planning_ru_model.joblib")
    try:
        return load_model(model_path)
    except FileNotFoundError as exc:
        raise RuntimeError(f"model file not found: {model_path}") from exc


@app.get("/health")
def health() -> dict[str, str]:
    try:
        model = get_model()
        model_version = model.version or "unknown/local"
    except RuntimeError:
        model_version = "unavailable"
    return {"status": "ok", "model_version": model_version, "parser_version": app.version}


@app.post("/parse", response_model=ParseResponse)
def parse(request: ParseRequest) -> ParseResponse:
    try:
        model = get_model()
    except RuntimeError as exc:
        raise HTTPException(status_code=503, detail=str(exc)) from exc
    return model.predict(request.text, request.base_time)
