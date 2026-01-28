FROM golang:1.25-alpine3.22@sha256:2dcdadabb270f820015c81a92dea242504351af86f8baaa60d234685ba083015 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o ole-gate-count main.go

FROM alpine:3.23@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659

ARG \
  # renovate: datasource=repology depName=alpine_3_23/ca-certificates
  CA_CERTIFICATES_VERSION="20251003-r0" \
  # renovate: datasource=repology depName=alpine_3_23/curl
  CURL_VERSION="8.17.0-r1" \
  # renovate: datasource=repology depName=alpine_3_23/jq
  JQ_VERSION="1.8.1-r0" \
  # renovate: datasource=repology depName=alpine_3_23/tzdata
  TZDATA_VERSION="2025c-r0"

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
