# Changelog

All notable changes to Frankfurter will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Add healthcheck to Dockerfile

### Changed

- Return latest rates for future rates

### Removed

- Removed JSONP

## [1.0.0] - 2024-12-04

### Changed

- API versioning in URL path (v1)
- Migrated from PostgreSQL to SQLite
- Moved domain from <https://api.frankfurter.app> to <https://api.frankfurter.dev>. Former will continue serving the old
  unversioned paths.

[Unreleased]: https://github.com/lineofflight/frankfurter/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/lineofflight/frankfurter/releases/tag/v1.0.0
