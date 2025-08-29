FROM golang:1.25-alpine3.22 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o ole-gate-count main.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates curl tzdata jq
WORKDIR /app

RUN adduser -D -s /bin/sh app
COPY --from=builder /app/ole-gate-count ./
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
