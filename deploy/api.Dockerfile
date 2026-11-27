# syntax=docker/dockerfile:1
# Single-container API for the hosted demo (Render free tier). Build context: repository root.
#   docker build -f deploy/api.Dockerfile .
# Differences from backend/Dockerfile: bundles the model benchmarks, defaults to the embedded
# in-memory Redis and the in-process event simulator (the CPU vision pipeline needs more than
# a 512 MB / 0.1 CPU instance), and listens on $PORT when the platform provides it.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata wget && adduser -D -u 10001 app && mkdir -p /data && chown app /data
COPY --from=build /out/api /usr/local/bin/api
COPY ml/benchmarks/results.json /app/benchmarks/results.json
ENV VG_ENV=production \
    VG_DATA_DIR=/data \
    VG_REDIS_URL=memory:// \
    VG_SIMULATOR=true \
    VG_AUTO_SEED=true \
    VG_BENCHMARKS_FILE=/app/benchmarks/results.json
USER app
EXPOSE 8080
ENTRYPOINT ["api"]
CMD ["serve"]
