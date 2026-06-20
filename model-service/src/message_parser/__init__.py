"""Planning message parser model package."""

__all__ = ["PlanningParserModel", "load_model"]


def __getattr__(name):
    if name == "PlanningParserModel":
        from message_parser.model import PlanningParserModel

        return PlanningParserModel
    if name == "load_model":
        from message_parser.model import load_model

        return load_model
    raise AttributeError(name)
