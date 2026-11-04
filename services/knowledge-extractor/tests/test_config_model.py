from __future__ import annotations

import os

import pytest

from knowledge_extractor import gliner
from knowledge_extractor.config import ConfigError, parse_settings, read_key
from knowledge_extractor.model import ModelHolder, ModelUnavailable

SECRET = "s3cr3t-value-that-must-not-leak"


def key_file(tmp_path, content=SECRET, mode=0o600):
    path = tmp_path / "extractor.key"
    path.write_text(content + "\n")
    os.chmod(path, mode)
    return str(path)


def test_key_file_is_read_and_stripped(tmp_path):
    assert read_key(key_file(tmp_path)) == SECRET.encode()


@pytest.mark.parametrize("mode", [0o640, 0o604, 0o644])
def test_key_file_must_be_private(tmp_path, mode):
    with pytest.raises(ConfigError) as caught:
        read_key(key_file(tmp_path, mode=mode))
    assert SECRET not in str(caught.value)


def test_short_or_missing_key(tmp_path):
    with pytest.raises(ConfigError):
        read_key(key_file(tmp_path, content="short"))
    with pytest.raises(ConfigError):
        read_key(str(tmp_path / "absent"))


def test_settings_defaults_to_loopback_and_cpu(tmp_path):
    settings = parse_settings(["--key-file", key_file(tmp_path)])
    assert (settings.host, settings.device) == ("127.0.0.1", "cpu")
    assert settings.limits.inference_concurrency == 1


def test_non_loopback_bind_needs_explicit_flags(tmp_path):
    path = key_file(tmp_path)
    with pytest.raises(ConfigError):
        parse_settings(["--key-file", path, "--host", "0.0.0.0"])
    with pytest.raises(ConfigError):
        parse_settings(["--key-file", path, "--host", "0.0.0.0", "--allow-non-loopback"])
    settings = parse_settings(["--key-file", path, "--host", "0.0.0.0", "--allow-non-loopback",
                               "--allowed-host", "knowledge-extractor"])
    assert settings.allowed_hosts == ("knowledge-extractor",)


def test_bad_limits_are_config_errors(tmp_path):
    with pytest.raises(ConfigError):
        parse_settings(["--key-file", key_file(tmp_path), "--window-tokens", "8",
                        "--window-margin", "8"])


@pytest.mark.parametrize("device", ["mps", "cuda"])
def test_unavailable_device_is_reported_without_fallback(monkeypatch, device):
    import torch

    monkeypatch.setattr(torch.backends.mps, "is_available", lambda: False)
    monkeypatch.setattr(torch.cuda, "is_available", lambda: False)
    holder = ModelHolder(lambda: gliner.GlinerModel.load(device, None))
    holder.start()
    holder.join(10)
    state, reason, model = holder.status()
    assert state == "error" and model is None
    assert reason.startswith(f"device {device} unavailable")


def test_missing_cache_explains_how_to_fetch(tmp_path, monkeypatch):
    monkeypatch.setenv("HF_HUB_OFFLINE", "1")
    with pytest.raises(ModelUnavailable) as caught:
        gliner.resolve_snapshot(str(tmp_path / "empty-cache"))
    message = str(caught.value)
    assert "not in the local Hugging Face cache" in message
    assert "scripts/fetch_knowledge_model.py" in message
    assert str(tmp_path) not in message


def test_incomplete_cache_is_reported(tmp_path):
    snapshot = (tmp_path / f"models--{gliner.MODEL_ID.replace('/', '--')}" / "snapshots"
                / gliner.MODEL_REVISION)
    snapshot.mkdir(parents=True)
    (snapshot / "config.json").write_text("{}")
    with pytest.raises(ModelUnavailable) as caught:
        gliner.resolve_snapshot(str(tmp_path))
    assert "incomplete" in str(caught.value)


def test_unexpected_loader_error_hides_details():
    def loader():
        raise RuntimeError("/private/path/with/secret")

    holder = ModelHolder(loader)
    holder.start()
    holder.join(5)
    state, reason, _ = holder.status()
    assert state == "error" and "RuntimeError" in reason and "/private" not in reason


def test_inference_concurrency_above_one_is_refused(tmp_path):
    with pytest.raises(ConfigError):
        parse_settings(["--key-file", key_file(tmp_path), "--inference-concurrency", "2"])


def test_gliner_module_forces_offline_before_hf_imports():
    import subprocess
    import sys

    env = {k: v for k, v in os.environ.items()
           if k not in ("HF_HUB_OFFLINE", "TRANSFORMERS_OFFLINE")}
    code = ("import os, sys; import knowledge_extractor.gliner; "
            "assert 'gliner2' not in sys.modules and 'huggingface_hub' not in sys.modules; "
            "import huggingface_hub.constants as c; "
            "print(os.environ['HF_HUB_OFFLINE'], os.environ['TRANSFORMERS_OFFLINE'], c.HF_HUB_OFFLINE)")
    out = subprocess.run([sys.executable, "-c", code], env=env, capture_output=True, text=True,
                         check=True)
    assert out.stdout.split() == ["1", "1", "True"]


def test_schema_cache_is_bounded_by_digest(schema):
    from knowledge_extractor.model import SchemaCache
    from knowledge_extractor.schemas import ExtractionSchema

    compiled = []

    def compile_schema(s):
        compiled.append(s)
        return object()

    cache = SchemaCache(compile_schema, size=2)
    first = cache.get(schema)
    assert cache.get(ExtractionSchema.model_validate(schema.model_dump())) is first
    for i in range(3):
        cache.get(ExtractionSchema.model_validate({"entities": {f"t{i}": ""}}))
    assert len(cache) == 2 and len(compiled) == 4
    assert cache.get(schema) is not first  # evicted, compiled again
