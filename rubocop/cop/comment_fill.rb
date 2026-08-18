# frozen_string_literal: true

require "rubocop"

module RuboCop
  module Cop
    module Style
      class CommentFill < Base
        include RangeHelp
        extend AutoCorrector

        MSG = "Comment line wraps early; first word of next line fits within %<max>d characters."

        def on_new_investigation
          return if processed_source.comments.empty?

          max_length = cop_config.fetch("Max", 120)
          comments_by_line = processed_source.comments.to_h { |c| [c.loc.line, c] }

          comments_by_line.each do |line_no, comment|
            next_comment = comments_by_line[line_no + 1]
            next unless next_comment

            check_comment_pair(comment, next_comment, max_length)
          end
        end

        private

        def check_comment_pair(comment, next_comment, max_length)
          return if comment.loc.column != next_comment.loc.column

          text1 = comment.text
          text2 = next_comment.text

          return if excluded_comment?(text1) || excluded_comment?(text2)

          first_word = extract_first_word(text2)
          return if first_word.nil? || first_word.empty?

          current_len = text1.length
          col = comment.loc.column
          total_len = col + current_len + 1 + first_word.length

          return if total_len > max_length

          add_offense(comment, message: format(MSG, max: max_length)) do |corrector|
            autocorrect_comment_pair(corrector, comment, next_comment, first_word)
          end
        end

        def excluded_comment?(text)
          return true if text == "#" || text.strip == "#"
          return true if text.start_with?("# rubocop:", "# frozen_string_literal:", "# encoding:")

          content = text.sub(/^#\s?/, "")
          return true if content.start_with?("- ", "* ", "> ", "#") || content.match?(/^\d+\.\s/)
          return true if text.start_with?("#   ") # Indented code in comment

          false
        end

        def extract_first_word(text)
          content = text.sub(/^#\s*/, "")
          content.split(/\s+/).first
        end

        def autocorrect_comment_pair(corrector, comment, next_comment, word)
          corrector.insert_after(comment.loc.expression, " #{word}")

          # Remove word from next_comment
          next_text = next_comment.text
          new_next_text = next_text.sub(/(^#\s*)#{Regexp.escape(word)}\s*/, '\1')

          if new_next_text.strip == "#" || new_next_text.sub(/^#\s*/, "").empty?
            range = range_by_whole_lines(next_comment.loc.expression, include_final_newline: true)
            corrector.remove(range)
          else
            corrector.replace(next_comment.loc.expression, new_next_text)
          end
        end
      end
    end
  end
end
