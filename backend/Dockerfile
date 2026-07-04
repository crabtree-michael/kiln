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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kiln /usr/local/bin/kiln
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/kiln"]
