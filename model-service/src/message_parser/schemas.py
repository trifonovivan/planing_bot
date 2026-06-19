from __future__ import annotations

from typing import Dict, Optional

from pydantic import BaseModel, Field


class ParserOutput(BaseModel):
    title: Optional[str] = None
    due_at: Optional[str] = None
    remind_at: Optional[str] = None
    priority: Optional[str] = None
    category: Optional[str] = None
    assignee: Optional[str] = None
    repeat: Optional[str] = None
    status: str = "failed"
    clarification_reason: Optional[str] = None


class ParseResponse(BaseModel):
    output: ParserOutput
    confidence: float
    field_confidence: Dict[str, float] = Field(default_factory=dict)
    source: str = "hybrid"
    time_source: str = "none"


class ParseRequest(BaseModel):
    text: str = Field(min_length=1)
    base_time: Optional[str] = None
