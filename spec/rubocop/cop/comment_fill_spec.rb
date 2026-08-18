# frozen_string_literal: true

require_relative "../../helper"
require_relative "../../../rubocop/cop/comment_fill"

describe RuboCop::Cop::Style::CommentFill do
  let(:cop) { RuboCop::Cop::Style::CommentFill.new(config) }
  let(:config) { RuboCop::Config.new("Style/CommentFill" => { "Max" => 120 }) }

  def inspect_source(source)
    processed_source = RuboCop::ProcessedSource.new(source, 3.4, "file.rb")
    commissioner = RuboCop::Cop::Commissioner.new([cop], raise_error: true)
    offenses = commissioner.investigate(processed_source).offenses
    cop.define_singleton_method(:offenses) { offenses }
    processed_source
  end

  it "flags comment line when next comment's first word fits within line limit" do
    source = <<~RUBY
      # This is a comment line that wraps early
      # and could fit the next word on the previous line.
    RUBY
    inspect_source(source)

    assert_equal 1, cop.offenses.size
    assert_equal "Comment line wraps early; first word of next line fits within 120 characters.",
                 cop.offenses.first.message
  end

  it "does not flag comment line when next word exceeds line limit" do
    prefix = "# #{"a" * 115}"
    source = <<~RUBY
      #{prefix}
      # word
    RUBY
    inspect_source(source)

    assert_empty cop.offenses
  end

  it "ignores bullet lists, directives, bare separators, and code indentation" do
    source = <<~RUBY
      # - Item 1 line that wraps
      #   and continues here
      # rubocop:disable Metrics/AbcSize
      #
      # ## Markdown Header
      #   def code_example
      #     do_something
      #   end
    RUBY
    inspect_source(source)

    assert_empty cop.offenses
  end

  it "ignores comments at different indentation levels" do
    source = <<~RUBY
      # Outer comment that wraps
        # inner comment indented further
    RUBY
    inspect_source(source)

    assert_empty cop.offenses
  end

  it "ignores inline comments after code" do
    source = <<~RUBY
      "2225" => "USD", # DLS. USA BILLETE
      "1111" => "EUR", # EURO
    RUBY
    inspect_source(source)

    assert_empty cop.offenses
  end

  it "autocorrects by pulling single word up and removing empty comment line" do
    source = <<~RUBY
      # This is a comment line that wraps
      # early.
    RUBY
    inspect_source(source)

    assert_equal 1, cop.offenses.size
    assert_equal "# This is a comment line that wraps early.\n", cop.offenses.first.corrector.rewrite
  end

  it "autocorrects by pulling first word up and keeping remainder of next comment line" do
    source = <<~RUBY
      # This is line one that wraps
      # early and continues on line two.
    RUBY
    inspect_source(source)

    assert_equal 1, cop.offenses.size
    expected = <<~RUBY
      # This is line one that wraps early
      # and continues on line two.
    RUBY
    assert_equal expected, cop.offenses.first.corrector.rewrite
  end
end
