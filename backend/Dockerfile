# Kiln backend — single static Go binary (02 §3: "single static binary").
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads independently of source changes.
COPY go.mod go.sum* ./
RUN go mod download || true

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/kiln ./cmd/kiln

# Runtime stage. NOT distroless: the brain's `bash` tool
# (docs/superpowers/specs/2026-07-04-brain-repo-bash-tool-design.md) runs
# allowlisted shell commands in a local clone, so the image needs /bin/sh plus
# git, gh, ripgrep, and the coreutils busybox already provides (grep/find/ls/
# head/tail/sort/uniq/wc/awk/sed/cat). ca-certificates is required for the https
# clone + gh auth. The binary is CGO_ENABLED=0 static, so it runs on musl fine.
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
