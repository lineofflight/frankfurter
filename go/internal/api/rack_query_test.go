package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// plainRack turns parsed values into what encoding/json decodes, for comparing with Rack's output.
func plainRack(v any) any {
	switch v := v.(type) {
	case rackHash:
		m := map[string]any{}
		for k, x := range v {
			m[k] = plainRack(x)
		}
		return m
	case *[]any:
		out := []any{}
		for _, x := range *v {
			out = append(out, plainRack(x))
		}
		return out
	}
	return v
}

// Expectations from Rack 3.2.7's parse_nested_query (the parser Roda installs, depth limit 32).
func TestParseRackQueryLikeRack(t *testing.T) {
	for _, c := range []struct{ query, want string }{
		{"a[]=1&a[][b]=2", `{"a":["1",{"b":"2"}]}`},
		{"a[][b]=1&a[][b]=2", `{"a":[{"b":"1"},{"b":"2"}]}`},
		{"a[][b]=1&a[][c]=2", `{"a":[{"b":"1","c":"2"}]}`},
		{"a[b][c]=1&a[b][d]=2", `{"a":{"b":{"c":"1","d":"2"}}}`},
		{"a[b]=1&a[b]=2", `{"a":{"b":"2"}}`},
		{"a[][]=1", `{"a":[["1"]]}`},
		{"a[]x=1", `{"a":[{"x":"1"}]}`},
		{"a[x=1", `{"a":{"[x":"1"}}`},
		{"a[=1", `{"a[":"1"}`},
		{"a]=1", `{"a]":"1"}`},
		{"a[b]&a[c]=1", `{"a":{"b":null,"c":"1"}}`},
		{"x[][y][z]=1&x[][y][w]=2", `{"x":[{"y":{"z":"1","w":"2"}}]}`},
		{"x[][y][z]=1&x[][y][z]=2", `{"x":[{"y":{"z":"1"}},{"y":{"z":"2"}}]}`},
		{"a=1& b=2&  c=3", `{"a":"1","b":"2","c":"3"}`},
		{" a=1", `{" a":"1"}`},
		{"a[b]=1&a=2", `{"a":"2"}`},
		{"a[]=1&a[]=2&a[][x]=3", `{"a":["1","2",{"x":"3"}]}`},
		{"&&a=1&", `{"a":"1"}`},
		{"a[][b][]=1&a[][b][]=2", `{"a":[{"b":["1","2"]}]}`},
		{"a&a[]=1", `{"a":["1"]}`},
		{"a[]&a[]", `{"a":[null,null]}`},
		{"=1&[]=2&[a]=3", `{"[]":"2","[a]":"3"}`},
		{"a[[b]]=1", `{"a":{"[b":{"]":"1"}}}`},
		{"a[b]]=1", `{"a":{"b":{"]":"1"}}}`},
		{"a[][b]c=1", `{"a":[{"b":{"c":"1"}}]}`},
		{"a[][b=1", `{"a":[{"[b":"1"}]}`},
		{"a[]][b]=1", `{"a":[{"][b]":"1"}]}`},
		{"+a+=+b+", `{" a ":" b "}`},
		{"a[%5B]=1", `{"a":{"[":"1"}}`},
		{"captures[]=x&captures[][y]=1", `{"captures":["x",{"y":"1"}]}`},
		{"a[][b][c]=1&a[][b][c]=2&a[][b][d]=3", `{"a":[{"b":{"c":"1"}},{"b":{"c":"2","d":"3"}}]}`},
	} {
		got, err := parseRackQuery(c.query)
		if err != nil {
			t.Errorf("%s: %v", c.query, err)
			continue
		}
		var want any
		if err := json.Unmarshal([]byte(c.want), &want); err != nil {
			t.Fatal(err)
		}
		if g := plainRack(got); !reflect.DeepEqual(g, want) {
			t.Errorf("%s: got %v, Rack %s", c.query, g, c.want)
		}
	}
}

func TestParseRackQueryErrors(t *testing.T) {
	for _, query := range []string{
		"a[b]=1&a[b][c]=2",
		"x[y][z]=1&x[y][z][]=2",
		"a=1&a[b]=2",
		"a[b][]=1&a[b][]=2&a[b][c]=3",
		"a[b]=1&a[b][]=2",
		"a[b]=%zz",
		"a" + strings.Repeat("[x]", 32) + "=1",
		strings.Repeat("a=1&", 4096) + "a=1",
	} {
		if _, err := parseRackQuery(query); err == nil {
			t.Errorf("%.60s: want an error", query)
		}
	}
	for _, query := range []string{
		"a" + strings.Repeat("[x]", 31) + "=1",
		strings.TrimSuffix(strings.Repeat("a=1&", 4096), "&"),
	} {
		if _, err := parseRackQuery(query); err != nil {
			t.Errorf("%.60s: %v", query, err)
		}
	}
}
