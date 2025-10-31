FROM ruby:3.4.7

RUN mkdir /app
WORKDIR /app
ADD Gemfile /app/Gemfile
ADD Gemfile.lock /app/Gemfile.lock
RUN gem install bundler
RUN bundle config set without "development test"
RUN bundle install --jobs=8
ADD . /app

HEALTHCHECK --interval=2s --timeout=4s --start-period=3s --retries=15 \
    CMD curl -f "http://localhost:8080" || exit 1

CMD ["bundle", "exec", "unicorn", "-c", "./config/unicorn.rb"]
