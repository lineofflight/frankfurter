# Frankfurter

[Frankfurter](https://frankfurter.dev) is an open-source currency data API that tracks daily exchange rates from institutional sources.

## Deployment

The public API runs at <https://api.frankfurter.dev>. If you prefer to host your own instance, you can run Frankfurter with Docker.

### Using Docker

The quickest way to get started:

```bash
docker run -d -p 8080:8080 lineofflight/frankfurter
```

For production, mount a volume to persist the SQLite database across container restarts and set any optional API keys:

```bash
docker run -d -p 8080:8080 \
  -e DATABASE_URL=sqlite:///app/data/frankfurter.sqlite3 \
  -e FRED_API_KEY=your_key \
  -e TCMB_API_KEY=your_key \
  -v ./data:/app/data \
  --pull always \
  lineofflight/frankfurter
```

Without a mounted volume, the database is ephemeral and some endpoints may return empty data until their initial backfill completes.

Two data providers require API keys. Both are free and optional:

- `FRED_API_KEY` — Federal Reserve data. Register at [fred.stlouisfed.org](https://fred.stlouisfed.org/docs/api/api_key.html).
- `TCMB_API_KEY` — Turkish Central Bank data. Register at [evds3.tcmb.gov.tr](https://evds3.tcmb.gov.tr).

## Contributing

See [AGENTS.md](AGENTS.md) for development setup and guidelines.

Built a library or tool with Frankfurter? Share it in [Show and Tell](https://github.com/lineofflight/frankfurter/discussions/categories/show-and-tell)
