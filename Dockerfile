# --- Build stage: compile gems with the full toolchain ---
FROM ruby:4.0.5-slim AS builder

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    build-essential \
    libyaml-dev && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY Gemfile Gemfile.lock mise.toml ./

RUN gem install bundler && \
    bundle config set --local deployment 'true' && \
    bundle config set --local without 'development test' && \
    bundle install

# --- Runtime stage: only what the app needs to run ---
FROM ruby:4.0.5-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    curl \
    libyaml-0-2 && \
    rm -rf /var/lib/apt/lists/*

RUN useradd -m -u 1000 frankfurter

RUN mkdir /app && chown frankfurter:frankfurter /app
WORKDIR /app

# Copy the compiled bundle (gems + bundler config) from the builder
COPY --from=builder --chown=frankfurter:frankfurter /usr/local/bundle /usr/local/bundle
COPY --from=builder --chown=frankfurter:frankfurter /app/vendor /app/vendor

COPY --chown=frankfurter:frankfurter . .

ENV APP_ENV=production
ENV PORT=8080

USER frankfurter

HEALTHCHECK --interval=2s --timeout=4s --start-period=3s --retries=15 \
    CMD curl -f "http://localhost:${PORT:-8080}" || exit 1

CMD ["sh", "-c", "bundle exec rake db:setup && exec bundle exec foreman start"]
