# syntax=docker/dockerfile:1
# Sidecar image (docs/sidecars.md). Base images are pinned by multi-arch digest; Dependabot bumps them.
# Run read-only: docker run --read-only --tmpfs /tmp --memory 256m ...
FROM ghcr.io/astral-sh/uv:0.13.6@sha256:af22a52f5eafef3d78e551599d405c02df61919d18028f0cea8eda93bc9986d0 AS uv

FROM python:3.15-slim@sha256:94d3426f1def7db4a106a232ac121bfd975393e5dfd40a619b955fae45fe7913 AS build
COPY --from=uv /uv /bin/uv
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy UV_PYTHON_DOWNLOADS=never
WORKDIR /app
# The lockfile pins the exact upstream release and its hashes; --frozen never re-resolves.
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project
# Canary only (.github/workflows/sidecar-canary.yml): the upstream's default branch instead of
# the locked registry release, e.g. UPSTREAM_GIT=git+https://github.com/<owner>/<repo>.
ARG UPSTREAM_GIT=""
RUN if [ -n "$UPSTREAM_GIT" ]; then uv pip install --python .venv/bin/python "$UPSTREAM_GIT"; fi
COPY src ./src

# Unit tests on synthetic cassettes, run by scripts/sidecar-check.sh as `docker build --target test`.
FROM build AS test
COPY tests ./tests
COPY testdata ./testdata
RUN .venv/bin/python -m unittest discover -s tests

FROM python:3.15-slim@sha256:94d3426f1def7db4a106a232ac121bfd975393e5dfd40a619b955fae45fe7913
LABEL org.opencontainers.image.title="vitamux-sidecar-garmin" \
      org.opencontainers.image.source="https://github.com/KaanEmec/vitamux" \
      org.opencontainers.image.licenses="MIT AND MPL-2.0"
RUN useradd --system --uid 65532 --no-create-home --shell /usr/sbin/nologin sidecar
WORKDIR /app
COPY --from=build /app/.venv ./.venv
COPY --from=build /app/src ./src
# License texts of the upstream and its dependencies travel with the image.
COPY UPSTREAM.md /usr/share/doc/vitamux-sidecar/UPSTREAM.md
USER 65532
ENV PATH=/app/.venv/bin:$PATH PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD ["python", "-c", "import os,urllib.request as u; u.urlopen('http://127.0.0.1:'+os.environ.get('SIDECAR_ADDR','0.0.0.0:8080').rpartition(':')[2]+'/healthz', timeout=3)"]
CMD ["python", "-m", "src.main"]
