# Example sidecar image: the standard library only, so nothing is installed. The base image is
# pinned by multi-arch index digest; Dependabot bumps it weekly. By hand: look up the digest of
# python:3.12-slim in the registry. Build from this directory: docker build -t vitamux-sidecar-example .
FROM python:3.14-slim@sha256:a2b82f3c48559aa0a8446d9af49826b6e2b2016f4cd2afabfe6013ec53729170
ENV PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1
WORKDIR /app
COPY sidecar.py .
# Same uid as the vitamux image, so it can read the 0600 secret file the two share.
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD ["python", "-c", "import urllib.request as u; u.urlopen('http://127.0.0.1:8080/healthz', timeout=2)"]
CMD ["python", "sidecar.py"]
