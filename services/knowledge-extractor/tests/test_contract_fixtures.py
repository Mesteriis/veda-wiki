"""The Go client fixtures are wire contract v1; drift on either side must fail here."""
from __future__ import annotations

import json
from pathlib import Path

from starlette.testclient import TestClient

from fakes import FakeModel
from knowledge_extractor.app import create_app
from knowledge_extractor.limits import Limits
from knowledge_extractor.model import ModelHolder
from knowledge_extractor.schemas import ExtractRequest, ExtractResponse

REPO_ROOT = Path(__file__).resolve().parents[3]
FIXTURES = REPO_ROOT / "internal" / "knowledge" / "extractorclient" / "testdata"
KEY = "k" * 32


def load(name: str) -> dict:
    return json.loads((FIXTURES / name).read_text(encoding="utf-8"))


def shape(value):
    """Keys and value kinds, recursively; list items must share one shape."""
    if isinstance(value, dict):
        return {key: shape(item) for key, item in value.items()}
    if isinstance(value, list):
        shapes = []
        for item in value:
            if shape(item) not in shapes:
                shapes.append(shape(item))
        return shapes
    if isinstance(value, bool):
        return "bool"
    if isinstance(value, (int, float)) or value is None:
        return "number"  # confidences may be null; ints and floats are both JSON numbers
    return type(value).__name__


def test_request_fixture_is_a_valid_request():
    ExtractRequest.model_validate(load("extract_request.json"))


def test_response_fixture_is_a_valid_response():
    ExtractResponse.model_validate(load("extract_response.json"))


def test_fake_model_output_has_the_response_fixture_shape():
    model = FakeModel({"billing": "service", "db1": "host", "Redis": "technology"},
                      [("runs_on", "billing", "db1"), ("uses", "billing", "Redis")])
    holder = ModelHolder(lambda: model)
    holder.start()
    holder.join(5)
    app = create_app(holder, KEY.encode(), Limits(), allowed_hosts=["testserver"])
    request = load("extract_request.json")
    with TestClient(app) as client:
        response = client.post("/v1/extract", content=json.dumps(request), headers={
            "X-Veda-Extractor-Key": KEY, "Content-Type": "application/json"})
    assert response.status_code == 200, response.text
    body = response.json()
    ExtractResponse.model_validate(body)
    expected = load("extract_response.json")
    assert body["request_id"] == expected["request_id"]
    assert [b["id"] for b in body["blocks"]] == [b["id"] for b in expected["blocks"]]
    assert shape(body["model"]) == shape(expected["model"])
    # Compare the richest block (entities and relations present) structurally.
    assert shape(body["blocks"][1]) == shape(expected["blocks"][1])
