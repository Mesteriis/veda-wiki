from __future__ import annotations

import json
import threading

import pytest
from starlette.testclient import TestClient

from conftest import request_body
from fakes import FakeModel
from knowledge_extractor.app import create_app
from knowledge_extractor.limits import Limits
from knowledge_extractor.model import ModelHolder, ModelUnavailable

KEY = b"k" * 32
HEADERS = {"X-Veda-Extractor-Key": KEY.decode()}
SECRET_TEXT = "Секретный текст статьи 🚀 billing"


def ready_holder(model):
    holder = ModelHolder(lambda: model)
    holder.start()
    holder.join(5)
    assert holder.status()[0] == "ready"
    return holder


def client_for(model=None, limits=Limits(), holder=None):
    model = model or FakeModel({"billing": "service", "db-01": "host"},
                               [("runs_on", "billing", "db-01")])
    holder = holder or ready_holder(model)
    app = create_app(holder, KEY, limits, allowed_hosts=["testserver"])
    return TestClient(app)


def post(client, body, headers=HEADERS):
    return client.post("/v1/extract", content=json.dumps(body), headers={
        **headers, "Content-Type": "application/json"})


def test_health_is_always_ok():
    holder = ModelHolder(lambda: (_ for _ in ()).throw(ModelUnavailable("no")))
    with client_for(holder=holder) as client:
        assert client.get("/health").json() == {"status": "ok"}


def test_ready_reports_loading_then_ready():
    gate = threading.Event()
    model = FakeModel({"Alice Smith": "person"})

    def loader():
        gate.wait(5)
        return model

    holder = ModelHolder(loader)
    holder.start()
    with client_for(holder=holder) as client:
        response = client.get("/ready")
        assert response.status_code == 503 and response.json()["status"] == "loading"
        assert post(client, request_body([("a", "x")])).json()["error"]["code"] == "not_ready"
        gate.set()
        holder.join(5)
        ready = client.get("/ready")
        assert ready.status_code == 200
        assert ready.json()["model"] == {"id": "fake", "revision": "0", "device": "cpu"}


def test_ready_fails_when_self_test_finds_nothing():
    holder = ModelHolder(lambda: FakeModel({}))
    holder.start()
    holder.join(5)
    with client_for(holder=holder) as client:
        body = client.get("/ready").json()
        assert body == {"status": "error", "reason": "self-test produced no valid entity span"}


def test_missing_or_wrong_key_is_rejected():
    with client_for() as client:
        body = request_body([("a", SECRET_TEXT)])
        assert post(client, body, headers={}).status_code == 401
        wrong = post(client, body, headers={"X-Veda-Extractor-Key": "x" * 32})
        assert wrong.status_code == 401
        assert wrong.json()["error"]["code"] == "unauthorized"


def test_successful_extract_matches_contract():
    with client_for() as client:
        response = post(client, request_body([("p1", "billing runs on db-01.")]))
        assert response.status_code == 200
        data = response.json()
        assert set(data) == {"schema_version", "request_id", "model", "blocks"}
        block = data["blocks"][0]
        assert set(block) == {"id", "chunks", "entities", "relations", "dropped"}
        assert block["chunks"] == [{"start": 0, "end": 22, "feasible": True, "status": "ok"}]
        assert set(block["entities"][0]) == {"local_id", "type", "start", "end", "text", "confidence"}
        assert block["relations"] == [{"type": "runs_on", "head": "e1", "tail": "e2",
                                       "confidence": 0.8, "head_confidence": 0.9,
                                       "tail_confidence": 0.9}]


@pytest.mark.parametrize("mutate,code", [
    (lambda b: b.update(extra=1), "invalid_request"),
    (lambda b: b.update(schema_version=2), "invalid_request"),
    (lambda b: b["schema"]["relations"]["runs_on"].update(head=["robot"]), "invalid_request"),
    (lambda b: b["schema"]["entities"].update({"Bad Type": ""}), "invalid_request"),
    (lambda b: b["blocks"].append(dict(b["blocks"][0])), "invalid_request"),
    (lambda b: b["blocks"][0].update(text=""), "invalid_request"),
    (lambda b: b["blocks"][0].update(id="has space"), "invalid_request"),
    (lambda b: b.update(limits={"max_entities_per_block": 0}), "invalid_request"),
])
def test_invalid_requests_are_422_without_echoing_text(mutate, code):
    with client_for() as client:
        body = request_body([("a", SECRET_TEXT)])
        mutate(body)
        response = post(client, body)
        assert response.status_code == 422
        assert response.json()["error"]["code"] == code
        assert "Секретный" not in response.text and "billing" not in response.text


def test_lone_surrogate_is_rejected():
    with client_for() as client:
        raw = json.dumps(request_body([("a", "PLACEHOLDER")])).replace("PLACEHOLDER", "\\ud83d")
        response = client.post("/v1/extract", content=raw,
                               headers={**HEADERS, "Content-Type": "application/json"})
        assert response.status_code == 422


def test_body_block_count_and_length_limits():
    limits = Limits(max_body_bytes=2000, max_blocks=2, max_block_chars=50)
    with client_for(limits=limits) as client:
        big = post(client, request_body([("a", "x" * 3000)]))
        assert big.status_code == 413 and big.json()["error"]["code"] == "body_too_large"
        many = post(client, request_body([("a", "x"), ("b", "y"), ("c", "z")]))
        assert many.status_code == 413 and many.json()["error"]["code"] == "too_many_blocks"
        long = post(client, request_body([("a", "ж" * 51)]))
        assert long.status_code == 413 and long.json()["error"]["code"] == "block_too_long"
        exact = post(client, request_body([("a", "ж" * 50)]))
        assert exact.status_code == 200


def test_streamed_body_without_length_is_limited():
    limits = Limits(max_body_bytes=500)
    with client_for(limits=limits) as client:
        def chunks():
            yield b'{"schema_version": 1, "blocks": [{"id": "a", "text": "'
            for _ in range(100):
                yield b"x" * 50
            yield b'"}]}'

        response = client.post("/v1/extract", content=chunks(),
                               headers={**HEADERS, "Content-Type": "application/json"})
        assert response.status_code == 413


def test_wrong_content_type():
    with client_for() as client:
        response = client.post("/v1/extract", content=b"{}", headers=HEADERS)
        assert response.status_code == 415


def test_unknown_host_header_is_rejected():
    with client_for() as client:
        assert client.get("/health", headers={"Host": "evil.example"}).status_code == 400


def test_timeout_returns_504_and_stops_remaining_chunks():
    model = FakeModel({"billing": "service", "Alice Smith": "person"}, delay=0.05)
    limits = Limits(request_timeout=0.3, window_tokens=40, window_margin=2)
    with client_for(model, limits) as client:
        text = " ".join(["billing works."] * 200)
        model.calls.clear()
        response = post(client, request_body([("a", text)]))
        assert response.status_code == 504
        assert response.json()["error"]["code"] == "timeout"
    assert len(model.calls) < 20


def test_busy_when_the_inference_slot_stays_taken():
    release = threading.Event()
    model = FakeModel({"billing": "service", "Alice Smith": "person"})
    holder = ready_holder(model)
    model.release = release
    model.calls.clear()  # the loader's self-test already called the model
    limits = Limits(request_timeout=0.5, inference_concurrency=1)
    with client_for(holder=holder, limits=limits) as client:
        results = {}
        first = threading.Thread(target=lambda: results.update(
            first=post(client, request_body([("a", "billing.")]))))
        first.start()
        while not model.calls:
            threading.Event().wait(0.01)
        second = post(client, request_body([("b", "billing.")]))
        release.set()
        first.join(5)
        assert second.status_code == 503 and second.json()["error"]["code"] == "busy"
        assert results["first"].status_code in (200, 504)


def test_schema_too_large_for_window():
    limits = Limits(window_tokens=25, window_margin=1)
    with client_for(limits=limits) as client:
        response = post(client, request_body([("a", "billing " * 100)]))
        assert response.status_code == 422
        assert response.json()["error"]["code"] == "schema_too_large"
