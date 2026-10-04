# syntax=docker/dockerfile:1
# Sidecar image (docs/sidecars.md). node:22.23.3-alpine (22 LTS), pinned by digest; Dependabot bumps it.
FROM node:26-alpine@sha256:0b36e8c136b94cd4fcf02188228e76c31ad5872eef3fec8cbd2eee500cfd9e80 AS build
ENV NODE_ENV=production
WORKDIR /app
# The lockfile pins the exact upstream release and its hashes; npm ci never re-resolves.
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts && npm cache clean --force
# Canary only (.github/workflows/sidecar-canary.yml): the upstream from git instead of the locked release.
ARG UPSTREAM_GIT=""
RUN if [ -n "$UPSTREAM_GIT" ]; then npm install --no-save --ignore-scripts "$UPSTREAM_GIT"; fi
COPY src ./src

# Unit tests on synthetic cassettes, run by scripts/sidecar-check.sh as `docker build --target test`.
FROM build AS test
ENV NODE_ENV=development
COPY tests ./tests
COPY testdata ./testdata
RUN node --test

FROM node:26-alpine@sha256:0b36e8c136b94cd4fcf02188228e76c31ad5872eef3fec8cbd2eee500cfd9e80
LABEL org.opencontainers.image.title="vitamux-sidecar-whoop" \
      org.opencontainers.image.source="https://github.com/KaanEmec/vitamux" \
      org.opencontainers.image.licenses="MIT"
ENV NODE_ENV=production
WORKDIR /app
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/src ./src
COPY package.json ./
# Notices of the upstream travel with the image.
COPY UPSTREAM.md /usr/share/doc/vitamux-sidecar/UPSTREAM.md
# Runs as the image's non-root user and writes nothing: works with a read-only root filesystem.
USER node
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD node -e "fetch('http://127.0.0.1:8080/healthz').then(r=>process.exit(r.ok?0:1),()=>process.exit(1))"
CMD ["node", "src/main.js"]
