// Command vcrconvert converts the Ruby VCR cassettes in spec/vcr_cassettes into go-vcr v4 cassettes.
//
//	go run ./cmd/vcrconvert -in ../spec/vcr_cassettes -out testdata/cassettes
//
// Bodies are decoded the way Ruby's VCR decodes them on playback: Psych's !binary scalars are base64, and a plain
// string is transcoded to its recorded encoding when that encoding is a single-byte one (ISO-8859-1). With -sums the
// command instead prints the size and SHA-256 of every body as go-vcr loads it back, for comparison with Ruby.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v4"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
)

func main() {
	in := flag.String("in", "../spec/vcr_cassettes", "directory of Ruby VCR cassettes (*.yml)")
	out := flag.String("out", "testdata/cassettes", "directory for go-vcr cassettes (*.yaml)")
	sums := flag.Bool("sums", false, "print body sizes and SHA-256 of the converted cassettes instead of converting")
	flag.Parse()

	if *sums {
		if err := printSums(*out); err != nil {
			log.Fatal(err)
		}
		return
	}

	paths, err := filepath.Glob(filepath.Join(*in, "*.yml"))
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, path := range paths {
		if err := convert(path, *out); err != nil {
			log.Fatalf("%s: %v", path, err)
		}
	}
	log.Printf("converted %d cassettes", len(paths))
}

func convert(path, outDir string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	root := doc.Content[0]
	list := lookup(root, "http_interactions")
	if list == nil {
		return fmt.Errorf("no http_interactions")
	}

	name := strings.TrimSuffix(filepath.Base(path), ".yml")
	c := cassette.New(filepath.Join(outDir, name))
	c.MarshalFunc = yaml.Marshal
	for _, node := range list.Content {
		i, err := interaction(node)
		if err != nil {
			return err
		}
		c.AddInteraction(i)
	}
	return c.Save()
}

func interaction(node *yaml.Node) (*cassette.Interaction, error) {
	reqNode, resNode := lookup(node, "request"), lookup(node, "response")
	if reqNode == nil || resNode == nil {
		return nil, fmt.Errorf("interaction without request or response")
	}

	rawURL := scalar(lookup(reqNode, "uri"))
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	reqBody, err := body(lookup(reqNode, "body"))
	if err != nil {
		return nil, err
	}
	resBody, err := body(lookup(resNode, "body"))
	if err != nil {
		return nil, err
	}

	status := lookup(resNode, "status")
	code, err := strconv.Atoi(scalar(lookup(status, "code")))
	if err != nil {
		return nil, fmt.Errorf("status code: %w", err)
	}
	message := scalar(lookup(status, "message"))
	if message == "" {
		message = http.StatusText(code)
	}

	resHeaders := headers(lookup(resNode, "headers"))
	// Bodies are stored decoded, and transcoding may change their length.
	for _, h := range []string{"Content-Length", "Content-Encoding", "Transfer-Encoding"} {
		resHeaders.Del(h)
	}

	return &cassette.Interaction{
		Request: cassette.Request{
			Proto:         "HTTP/1.1",
			ProtoMajor:    1,
			ProtoMinor:    1,
			ContentLength: int64(len(reqBody)),
			Host:          u.Host,
			Body:          reqBody,
			Headers:       headers(lookup(reqNode, "headers")),
			URL:           rawURL,
			Method:        strings.ToUpper(scalar(lookup(reqNode, "method"))),
		},
		Response: cassette.Response{
			Proto:         "HTTP/1.1",
			ProtoMajor:    1,
			ProtoMinor:    1,
			ContentLength: int64(len(resBody)),
			Body:          resBody,
			Headers:       resHeaders,
			Status:        fmt.Sprintf("%d %s", code, message),
			Code:          code,
		},
	}, nil
}

// body mirrors VCR's body_from/try_encode_string.
func body(node *yaml.Node) (string, error) {
	if node == nil {
		return "", nil
	}
	encoding := scalar(lookup(node, "encoding"))
	if b64 := lookup(node, "base64_string"); b64 != nil {
		raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64.Value), ""))
		return string(raw), err
	}
	str := lookup(node, "string")
	if str == nil {
		return "", nil
	}
	if str.Tag == "!binary" {
		// Psych yields a binary string; re-encoding it to anything but binary fails in VCR, which then keeps the bytes.
		raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(str.Value), ""))
		return string(raw), err
	}

	switch strings.ToUpper(encoding) {
	case "", "UTF-8", "US-ASCII", "ASCII-8BIT", "BINARY":
		// Transcoding UTF-8 to US-ASCII either leaves the bytes alone or fails, and VCR keeps the bytes on failure.
		return str.Value, nil
	case "ISO-8859-1":
		return latin1(str.Value), nil
	default:
		return "", fmt.Errorf("unsupported body encoding %q", encoding)
	}
}

// latin1 transcodes UTF-8 to ISO-8859-1, returning s unchanged when a rune has no Latin-1 form (as Ruby's
// String#encode raises and VCR keeps the original).
func latin1(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF || r == utf8.RuneError {
			return s
		}
		out = append(out, byte(r))
	}
	return string(out)
}

func headers(node *yaml.Node) http.Header {
	h := http.Header{}
	if node == nil || node.Kind != yaml.MappingNode {
		return h
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i].Value, node.Content[i+1]
		if val.Kind == yaml.SequenceNode {
			for _, v := range val.Content {
				h[key] = append(h[key], v.Value)
			}
		} else {
			h[key] = append(h[key], val.Value)
		}
	}
	return h
}

func lookup(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func scalar(node *yaml.Node) string {
	if node == nil || node.Tag == "!!null" {
		return ""
	}
	return node.Value
}

func printSums(dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		c, err := cassette.Load(strings.TrimSuffix(path, ".yaml"))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for i, in := range c.Interactions {
			for _, part := range []struct{ kind, body string }{{"req", in.Request.Body}, {"res", in.Response.Body}} {
				fmt.Printf("%s %d %s %d %x\n", name, i, part.kind, len(part.body), sha256.Sum256([]byte(part.body)))
			}
		}
	}
	return nil
}
