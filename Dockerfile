FROM golang:1.25-alpine3.22@sha256:aee43c3ccbf24fdffb7295693b6e33b21e01baec1b2a55acc351fde345e9ec34 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o ole-gate-count main.go

FROM alpine:3.22@sha256:4b7ce07002c69e8f3d704a9c5d6fd3053be500b7f1c69fc0d80990c2ad8dd412

ARG \
  # renovate: datasource=repology depName=alpine_3_22/ca-certificates
  CA_CERTIFICATES_VERSION="20250911-r0" \
  # renovate: datasource=repology depName=alpine_3_22/curl
  CURL_VERSION="8.14.1-r2" \
  # renovate: datasource=repology depName=alpine_3_22/jq
  JQ_VERSION="1.8.0-r0" \
  # renovate: datasource=repology depName=alpine_3_22/tzdata
  TZDATA_VERSION="2025b-r0"

RUN apk update && \
  apk --no-cache add \
    ca-certificates=="${CA_CERTIFICATES_VERSION}" \
    curl=="${CURL_VERSION}" \
    tzdata=="${TZDATA_VERSION}" \
    jq=="${JQ_VERSION}"
WORKDIR /app

RUN adduser -D -s /bin/sh app
COPY --from=builder /app/ole-gate-count ./
COPY templates/ ./templates/
COPY --chown=app:app . .

USER app

ENV \
  TZ=America/New_York \
  MARIADB_HOST=mariadb \
  MARIADB_USER=ole \
  MARIADB_NAME=ole \
  MARIADB_PORT=3306 \
  OLE_GATE_URLS= \
  PORT=8080 \
  SCRIPT_NAME=/gate-counts

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD curl -fs http://localhost:8080/health | jq -e .status | grep healthy

CMD ["/app/ole-gate-count"]
