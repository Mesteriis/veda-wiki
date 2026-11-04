# CPU image of the VEDA knowledge extractor (services/knowledge-extractor).
# Build context: services/knowledge-extractor; the "scripts" context adds the
# model fetch script. The model is never baked in: it lives in the model
# cache volume, filled once by the "fetch-model" service, and the extractor
# runs with HF_HUB_OFFLINE=1.
FROM python:3.12-slim@sha256:b18b261e1b4974b4f321451ad5251ded85abb22c2b8b262da527dda1d031ab30

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy \
    UV_PYTHON_DOWNLOADS=never

RUN pip install --no-cache-dir uv==0.12.11

WORKDIR /app
COPY pyproject.toml uv.lock README.md ./
COPY src ./src
# Linux resolves torch from the CPU-only index pinned in uv.lock.
RUN uv sync --frozen --no-dev --no-editable && rm -rf /root/.cache
COPY --from=scripts fetch_knowledge_model.py /app/fetch_knowledge_model.py

RUN groupadd --system --gid 10001 extractor \
    && useradd --system --uid 10001 --gid extractor --home-dir /nonexistent --shell /usr/sbin/nologin extractor \
    && mkdir -p /models \
    && chmod 1777 /models

ENV HF_HOME=/models \
    HF_HUB_CACHE=/models/hub \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    PATH=/app/.venv/bin:$PATH

# Compose runs the container as the invoking user (KNOWLEDGE_UID/GID) so
# that it can read the 0600 key file; /models is sticky and world-writable
# so that the cache volume works for any such user.
USER extractor
EXPOSE 8790
ENTRYPOINT ["knowledge-extractor"]
