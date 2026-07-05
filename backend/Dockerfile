# Kiln — single static Go binary with the frontend SPA embedded (design
# 2026-07-05, decision e/D2: client + server ship as one Render web service).
#
# IMPORTANT: this Dockerfile REQUIRES the repo ROOT as the build context (it
# copies both frontend/ and backend/). render.yaml must therefore set:
#     dockerContext: .          # repo root
#     dockerfilePath: backend/Dockerfile
# Building with `./backend` as context (the old layout) will fail at `COPY
# frontend/ ...`.

# ---------------------------------------------------------------------------
# Stage 1 — build the frontend and (when a token is present) upload source maps.
# @sentry/vite-plugin reads SENTRY_AUTH_TOKEN from the env; org/project/url are
# hardcoded in the frontend's vite config. VITE_SENTRY_DSN is baked into the
# bundle. All three are no-ops when their build args are empty (local builds).
# ---------------------------------------------------------------------------
FROM node:22-alpine AS frontend
WORKDIR /app/frontend

ARG VITE_SENTRY_DSN
ARG SENTRY_AUTH_TOKEN
ARG VERSION=dev
ENV VITE_SENTRY_DSN=${VITE_SENTRY_DSN} \
    SENTRY_AUTH_TOKEN=${SENTRY_AUTH_TOKEN} \
    VERSION=${VERSION}

# Install with the committed lockfile first (cache-friendly), then build.
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm build   # emits /app/frontend/dist

# ---------------------------------------------------------------------------
# Stage 2 — build the Go binary. The frontend dist is copied over the committed
# placeholder in internal/web/dist BEFORE the build so //go:embed picks up the
# real assets (02 §3: single static binary).
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads independently of source changes.
COPY backend/go.mod backend/go.sum* ./
RUN go mod download || true

COPY backend/ ./
# Replace the placeholder SPA with the real build output before embedding.
RUN rm -rf internal/web/dist
COPY --from=frontend /app/frontend/dist ./internal/web/dist

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/kiln ./cmd/kiln

# ---------------------------------------------------------------------------
# Stage 3 — runtime. NOT distroless: the brain's `bash` tool
# (docs/superpowers/specs/2026-07-04-brain-repo-bash-tool-design.md) runs
# allowlisted shell commands in a local clone, so the image needs /bin/sh plus
# git, gh, ripgrep, and the coreutils busybox already provides (grep/find/ls/
# head/tail/sort/uniq/wc/awk/sed/cat). ca-certificates is required for the https
# clone + gh auth. The binary is CGO_ENABLED=0 static, so it runs on musl fine.
# ---------------------------------------------------------------------------
FROM alpine:3.20
RUN apk add --no-cache git github-cli ripgrep ca-certificates \
    && adduser -D -H -u 10001 kiln \
    && mkdir -p /var/lib/kiln/repo \
    && chown -R kiln:kiln /var/lib/kiln
COPY --from=build /out/kiln /usr/local/bin/kiln
# KILN_REPO_DIR is where internal/repo clones the project repo at boot; keep it
# writable by the run user and aligned with the composition-root default.
ENV KILN_REPO_DIR=/var/lib/kiln/repo
USER kiln:kiln
ENTRYPOINT ["/usr/local/bin/kiln"]
