FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY . .
WORKDIR /build/scheduler
RUN go build -o /go-trader .

FROM python:3.12-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir pandas requests numpy ccxt
RUN mkdir -p /app/.venv/bin && ln -sf /usr/local/bin/python3 /app/.venv/bin/python3
WORKDIR /app
COPY --from=builder /build /app
COPY --from=builder /go-trader /app/scheduler/go-trader
RUN mkdir -p /app/scheduler/logs && chmod 777 /app/scheduler/logs
EXPOSE 8100
CMD ./scheduler/go-trader
