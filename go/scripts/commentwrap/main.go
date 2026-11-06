// Commentwrap rewraps full-line // comments to 80 columns, the width Go code
// conventionally uses for comments. gofmt never wraps comments, so this fills
// the gap the way RuboCop's Style/CommentFill does on the Ruby side.
//
// Only plain prose paragraphs are rewrapped. Code blocks (lines indented past
// "// "), list items, directives, example Output blocks, lines with aligned
// columns, and generated files are left as they are. Trailing comments after
// code are never touched.
//
// Usage:
//
//	go run ./scripts/commentwrap -w .   # rewrite files in place
//	go run ./scripts/commentwrap -l .   # list files that need rewrapping
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	width    = 80
	tabWidth = 4
)

var (
	comment   = regexp.MustCompile(`^(\s*)//(.*)$`)
	listItem  = regexp.MustCompile(`^(-|\*|\+|\d+[.)])\s`)
	directive = regexp.MustCompile(`^//(go:|line |export |extern |nolint|lint:|\+build)`)
	generated = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)
)

func main() {
	write := flag.Bool("w", false, "rewrite files in place")
	list := flag.Bool("l", false, "list files that need rewrapping")
	flag.Parse()
	if *write == *list {
		fmt.Fprintln(os.Stderr, "usage: commentwrap -w|-l path...")
		os.Exit(2)
	}

	var dirty []string
	for _, root := range flag.Args() {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); path != root && (name == "testdata" || strings.HasPrefix(name, ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out := Rewrap(src)
			if bytes.Equal(src, out) {
				return nil
			}
			dirty = append(dirty, path)
			if *write {
				return os.WriteFile(path, out, 0o644)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	for _, path := range dirty {
		fmt.Println(path)
	}
	if *list && len(dirty) > 0 {
		os.Exit(1)
	}
}

// Rewrap returns src with every prose paragraph of full-line comments
// rewrapped to width.
func Rewrap(src []byte) []byte {
	if generated.Match(src) {
		return src
	}
	lines := strings.Split(string(src), "\n")
	var out []string
	for i := 0; i < len(lines); {
		indent, text, ok := parse(lines[i])
		if ok && strings.Contains(text, "Output:") {
			// An example's expected output runs to the end of the comment
			// block and must stay byte for byte.
			for i < len(lines) {
				if _, _, ok := parse(lines[i]); !ok {
					break
				}
				out = append(out, lines[i])
				i++
			}
			continue
		}
		if !ok || !isProse(lines[i], text) {
			out = append(out, lines[i])
			i++
			continue
		}
		// Collect the paragraph: consecutive prose lines at the same indent.
		j := i
		var words []string
		long := false
		for j < len(lines) {
			ind, t, ok := parse(lines[j])
			if !ok || ind != indent || !isProse(lines[j], t) || strings.Contains(t, "Output:") {
				break
			}
			words = append(words, strings.Fields(t)...)
			long = long || columns(lines[j]) > width
			j++
		}
		// Paragraphs already within the width keep their line breaks, which
		// are sometimes deliberate.
		if long {
			out = append(out, fill(indent, words)...)
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	return []byte(strings.Join(out, "\n"))
}

// parse splits a full-line comment into its indent and its text after "// ".
func parse(line string) (indent, text string, ok bool) {
	m := comment.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// isProse reports whether a comment line is part of a rewrappable paragraph.
func isProse(line, text string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	switch {
	case directive.MatchString(trimmed):
		return false
	case !strings.HasPrefix(text, " "): // "//" alone, "//\tcode", "//go:"
		return false
	case strings.HasPrefix(text, "  "), strings.HasPrefix(text, " \t"): // code block
		return false
	}
	body := text[1:]
	switch {
	case body == "":
		return false
	case listItem.MatchString(body):
		return false
	case strings.HasPrefix(body, "#"): // doc heading
		return false
	case strings.Contains(body, "  "): // aligned columns
		return false
	case strings.Contains(body, "Output:"):
		return false
	}
	return true
}

// fill greedily packs words into comment lines no wider than width. A word
// longer than the line, such as a URL, gets a line of its own.
func fill(indent string, words []string) []string {
	prefix := indent + "// "
	start := columns(prefix)
	var lines []string
	var cur strings.Builder
	col := start
	for _, w := range words {
		n := utf8.RuneCountInString(w)
		if cur.Len() > 0 && col+1+n > width {
			lines = append(lines, prefix+cur.String())
			cur.Reset()
			col = start
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
			col++
		}
		cur.WriteString(w)
		col += n
	}
	if cur.Len() > 0 {
		lines = append(lines, prefix+cur.String())
	}
	return lines
}

// columns is the display width of s with tabs expanded to tabWidth.
func columns(s string) int {
	n := 0
	for _, r := range s {
		if r == '\t' {
			n += tabWidth - n%tabWidth
		} else {
			n++
		}
	}
	return n
}
