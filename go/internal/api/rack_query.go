package api

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Limits of the query parser Roda installs (Rack::QueryParser's defaults, with the depth indifferent_params sets).
const (
	rackDepthLimit    = 32
	rackParamsLimit   = 4096
	rackBytesizeLimit = 4 << 20
)

// rackHash is a query string parsed as Rack's parse_nested_query does. Values are strings, nil (a key without '='),
// *[]any (a key ending in []) or nested rackHash values (a key with [name] parts).
type rackHash map[string]any

var errRackTooDeep = errors.New("query nested too deep")

// parseRackQuery is Rack::QueryParser#parse_nested_query with Roda's separator: pairs split on '&' (and the spaces
// after it), '+' as space, a bad %-escape, a type conflict or a broken limit is an error.
func parseRackQuery(raw string) (rackHash, error) {
	params := rackHash{}
	if raw == "" {
		return params, nil
	}
	if len(raw) > rackBytesizeLimit {
		return nil, fmt.Errorf("total query size exceeds limit (%d)", rackBytesizeLimit)
	}
	pairs := strings.Split(raw, "&")
	if len(pairs) > rackParamsLimit {
		return nil, fmt.Errorf("total number of query parameters (%d) exceeds limit (%d)", len(pairs), rackParamsLimit)
	}
	for i, pair := range pairs {
		if i > 0 {
			pair = strings.TrimLeft(pair, " ")
		}
		if pair == "" {
			continue
		}
		k, v, hasValue := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(k)
		if err != nil {
			return nil, errors.New("invalid %-encoding (" + pair + ")")
		}
		var value any
		if hasValue {
			s, err := url.QueryUnescape(v)
			if err != nil {
				return nil, errors.New("invalid %-encoding (" + pair + ")")
			}
			value = s
		}
		if _, err := normalizeRackParams(params, key, value, 0); err != nil {
			return nil, err
		}
	}
	return params, nil
}

// normalizeRackParams is Rack's _normalize_params: it files v under name, nesting on its [] and [key] parts. It
// returns what the caller stores: params, a one-element array for a trailing [] below the top, or nil for an empty
// key.
func normalizeRackParams(params rackHash, name string, v any, depth int) (any, error) {
	if depth >= rackDepthLimit {
		return nil, errRackTooDeep
	}

	k, after := name, ""
	switch {
	case depth == 0:
		// At the top a leading [ is part of the key.
		if len(name) > 1 {
			if i := strings.IndexByte(name[1:], '['); i >= 0 {
				k, after = name[:i+1], name[i+1:]
			}
		}
	case strings.HasPrefix(name, "[]"):
		k, after = "[]", name[2:]
	case strings.HasPrefix(name, "["):
		if i := strings.IndexByte(name[1:], ']'); i >= 0 {
			k, after = name[1:i+1], name[i+2:]
		}
	}
	if k == "" {
		return nil, nil
	}

	switch {
	case after == "":
		if k == "[]" && depth != 0 {
			return &[]any{v}, nil
		}
		params[k] = v
	case after == "[":
		params[name] = v
	case after == "[]":
		arr, err := rackArrayAt(params, k)
		if err != nil {
			return nil, err
		}
		*arr = append(*arr, v)
	case strings.HasPrefix(after, "[]"):
		// x[][y] puts a hash inside the array; any other tail nests as it stands.
		child := after[2:]
		if len(after) >= 4 && after[2] == '[' && strings.HasSuffix(after, "]") {
			if c := after[3 : len(after)-1]; c != "" && !strings.ContainsAny(c, "[]") {
				child = c
			}
		}
		arr, err := rackArrayAt(params, k)
		if err != nil {
			return nil, err
		}
		var last rackHash
		if n := len(*arr); n > 0 {
			last, _ = (*arr)[n-1].(rackHash)
		}
		if last != nil && !rackHasKey(last, child) {
			if _, err := normalizeRackParams(last, child, v, depth+1); err != nil {
				return nil, err
			}
		} else {
			x, err := normalizeRackParams(rackHash{}, child, v, depth+1)
			if err != nil {
				return nil, err
			}
			*arr = append(*arr, x)
		}
	default:
		if params[k] == nil {
			params[k] = rackHash{}
		}
		h, ok := params[k].(rackHash)
		if !ok {
			return nil, rackTypeError("Hash", k, params[k])
		}
		x, err := normalizeRackParams(h, after, v, depth+1)
		if err != nil {
			return nil, err
		}
		params[k] = x
	}
	return params, nil
}

// rackArrayAt is params[k] ||= [], failing when k already holds something else.
func rackArrayAt(params rackHash, k string) (*[]any, error) {
	if params[k] == nil {
		params[k] = &[]any{}
	}
	arr, ok := params[k].(*[]any)
	if !ok {
		return nil, rackTypeError("Array", k, params[k])
	}
	return arr, nil
}

// rackHasKey is Rack's params_hash_has_key?: whether the path of names in key (a[b][c]) already exists in h.
func rackHasKey(h rackHash, key string) bool {
	if strings.Contains(key, "[]") {
		return false
	}
	var cur any = h
	for _, part := range strings.FieldsFunc(key, func(r rune) bool { return r == '[' || r == ']' }) {
		m, ok := cur.(rackHash)
		if !ok {
			return false
		}
		if cur, ok = m[part]; !ok {
			return false
		}
	}
	return true
}

func rackTypeError(want, k string, got any) error {
	kind := "String"
	switch got.(type) {
	case *[]any:
		kind = "Array"
	case rackHash:
		kind = "Hash"
	}
	return fmt.Errorf("expected %s (got %s) for param `%s'", want, kind, k)
}
