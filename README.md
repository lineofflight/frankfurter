# Frankfurter

[![Build](https://github.com/lineofflight/frankfurter/workflows/build/badge.svg)](https://github.com/lineofflight/frankfurter/actions)

[Frankfurter](https://frankfurter.dev) is a free and open-source currency data API that tracks reference exchange rates published by institutional and non-commercial sources like the European Central Bank. Check the website for a detailed walkthrough.

The API is publicly available at [https://api.frankfurter.dev][].

## Deployment

### Using Docker

The simplest way to run Frankfurter locally is with Docker:

```bash
docker run -d -p 80:8080 lineofflight/frankfurter
```

Once the container is running, open your browser and go to `http://localhost`.

## Libraries

* https://github.com/xuhcc/beancount-exchangerates is a [Beancount](https://beancount.github.io/) price source plugin using this API

## Contributing

Frankfurter is built with Ruby. To contribute:

1. Fork.
2. Install dependencies with `bundle install`.
3. Run tests with `bundle exec rake`.
4. Push your changes to a feature branch.
5. Open a pull request.

## Roadmap

- [x] Migrate to SQLite
- [x] Add API versioning in path
- [ ] Multiple Data Sources
- [ ] Add GraphQL endpoint
- [ ] Deploy as a blockchain oracle

### Reporting Missing Currencies

If you notice a currency missing from our data, please open an issue and include a suggested source with the missing data. We're looking for non-commercial sources like the European Central Bank that publish current and historical daily rates at the end of each working day.
