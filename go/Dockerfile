# Build from go/: docker build -t frankfurter go/
#
# One static binary (pure-Go SQLite, PDFium as WebAssembly, seeds, static files and CA intermediates embedded) on a
# distroless base that brings CA roots and tzdata. The container runs `frankfurter start`: migrate, seed, then the API
# and the scheduler in one process, as the Ruby image ran `rake db:setup && foreman start`. Maintenance tasks run the
# same binary: docker exec <container> frankfurter backfill ecb.

FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/frankfurter ./cmd/frankfurter && \
    mkdir -p /out/app/db

FROM gcr.io/distroless/static-debian12

# The database lives in /app/db (db/frankfurter_production.sqlite3), owned by uid 1000 as in the Ruby image, so an
# existing volume keeps working.
COPY --from=build --chown=1000:1000 /out/app /app
COPY --from=build /out/frankfurter /usr/local/bin/frankfurter

WORKDIR /app
ENV APP_ENV=production
ENV PORT=8080
USER 1000:1000
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=300s --retries=3 \
    CMD ["/usr/local/bin/frankfurter", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/frankfurter"]
CMD ["start"]
