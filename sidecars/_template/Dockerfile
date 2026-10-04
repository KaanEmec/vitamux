# syntax=docker/dockerfile:1
# Sidecar image template (docs/sidecars.md). Replace every <placeholder>; pin base images by
# multi-arch digest (`docker buildx imagetools inspect <image>:<tag>`), Dependabot keeps them current.
# This file is a Python example; for Node swap the base image and replace the install lines
# with `npm ci --omit=dev` (package.json + package-lock.json) and the final command with `node`.
FROM python:3.13-slim@sha256:<digest> AS build
COPY --from=ghcr.io/astral-sh/uv@sha256:<digest> /uv /usr/local/bin/uv
WORKDIR /app
# The lockfile pins the exact upstream release and its hashes; --frozen never re-resolves.
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project
# Canary only (.github/workflows/sidecar-canary.yml): the upstream's default branch instead of
# the locked registry release, e.g. UPSTREAM_GIT=git+https://github.com/<owner>/<repo>.
ARG UPSTREAM_GIT=""
RUN if [ -n "$UPSTREAM_GIT" ]; then uv pip install --python .venv/bin/python "$UPSTREAM_GIT"; fi
COPY src ./src

# Unit tests on synthetic cassettes (recorded upstream responses with fake values), run by
# scripts/sidecar-check.sh as `docker build --target test`. Never part of the shipped image.
FROM build AS test
COPY tests ./tests
COPY testdata ./testdata
RUN .venv/bin/python -m unittest discover -s tests

FROM python:3.13-slim@sha256:<digest>
LABEL org.opencontainers.image.title="vitamux-sidecar-<name>" \
      org.opencontainers.image.source="https://github.com/KaanEmec/vitamux" \
      org.opencontainers.image.licenses="MIT AND <upstream SPDX license>"
RUN useradd --system --uid 65532 --no-create-home sidecar
WORKDIR /app
COPY --from=build /app/.venv ./.venv
COPY --from=build /app/src ./src
# License texts of the upstream and its dependencies travel with the image.
COPY UPSTREAM.md /usr/share/doc/vitamux-sidecar/UPSTREAM.md
USER 65532
ENV PATH=/app/.venv/bin:$PATH PYTHONUNBUFFERED=1
EXPOSE 8080
CMD ["python", "-m", "src.main"]
