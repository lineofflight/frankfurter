FROM ruby:3.4.8-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    curl \
    build-essential \
    libyaml-dev && \
    rm -rf /var/lib/apt/lists/*

RUN useradd -m -u 1000 frankfurter

RUN mkdir /app && chown frankfurter:frankfurter /app
WORKDIR /app

COPY --chown=frankfurter:frankfurter Gemfile Gemfile.lock mise.toml ./

RUN gem install bundler && \
    bundle config set --local deployment 'true' && \
    bundle config set --local without 'development test' && \
    bundle install

COPY --chown=frankfurter:frankfurter . .

USER frankfurter

HEALTHCHECK --interval=2s --timeout=4s --start-period=3s --retries=15 \
    CMD curl -f "http://localhost:8080" || exit 1

CMD ["bundle", "exec", "unicorn", "-c", "./config/unicorn.rb"]
