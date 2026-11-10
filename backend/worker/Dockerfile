# Sentinel analysis worker.
FROM python:3.12-slim
COPY --from=ghcr.io/astral-sh/uv:0.12.23 /uv /usr/local/bin/uv
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy PYTHONUNBUFFERED=1
WORKDIR /app
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project
COPY sentinel_worker ./sentinel_worker
RUN uv sync --frozen --no-dev
RUN useradd --system --uid 10001 sentinel
USER sentinel
CMD ["/app/.venv/bin/python", "-m", "sentinel_worker.main"]
