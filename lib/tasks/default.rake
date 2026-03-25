# frozen_string_literal: true

task default: ["rubocop", "db:migrate", "db:seed", "spec"]
