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
  -e BAM_API_KEY=your_key \
  -e BANXICO_API_KEY=your_key \
  -e BCCH_USER=your_email \
  -e BCCH_PASS=your_password \
  -e BOT_API_KEY=your_key \
  -e FRED_API_KEY=your_key \
  -e TCMB_API_KEY=your_key \
  -v ./data:/app/data \
  --pull always \
  lineofflight/frankfurter
```

Without a mounted volume, the database is ephemeral and some endpoints may return empty data until their initial backfill completes.

Some data providers require API keys. All are free and optional:

- `BAM_API_KEY` — Bank Al-Maghrib (Morocco). Register at [apihelpdesk.centralbankofmorocco.ma](https://apihelpdesk.centralbankofmorocco.ma/apis).
- `BANXICO_API_KEY` — Banco de México. Register at [banxico.org.mx](https://www.banxico.org.mx/SieAPIRest/service/v1/).
- `BCCH_USER` / `BCCH_PASS` — Banco Central de Chile. Register at [si3.bcentral.cl](https://si3.bcentral.cl/Siete/es/Siete/API).
- `BOT_API_KEY` — Bank of Thailand. Register at [portal.api.bot.or.th](https://portal.api.bot.or.th).
- `FRED_API_KEY` — Federal Reserve. Register at [fred.stlouisfed.org](https://fred.stlouisfed.org/docs/api/api_key.html).
- `TCMB_API_KEY` — Turkish Central Bank. Register at [evds3.tcmb.gov.tr](https://evds3.tcmb.gov.tr).

## Contributing

See [AGENTS.md](AGENTS.md) for development setup and guidelines.

Built a library or tool with Frankfurter? Share it in [Show and Tell](https://github.com/lineofflight/frankfurter/discussions/categories/show-and-tell)
