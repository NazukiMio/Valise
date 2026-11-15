package isolated

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"

	"valise/core/profile"
)

var errReadinessJSON = errors.New("readiness metadata is invalid")

// uniqueJSON rejects ambiguous keys, trailing values and excessive nesting.
// No raw payload or decoder error is ever returned to the caller or a log.
func uniqueJSON(data []byte) (map[string]any, bool) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	budget := 65536
	value, err := jsonValue(d, 0, &budget)
	if err != nil {
		return nil, false
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, false
	}
	object, ok := value.(map[string]any)
	return object, ok
}

func jsonValue(d *json.Decoder, depth int, budget *int) (any, error) {
	*budget--
	if depth > 32 || *budget < 0 {
		return nil, errReadinessJSON
	}
	token, err := d.Token()
	if err != nil {
		return nil, errReadinessJSON
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for d.More() {
			token, err := d.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return nil, errReadinessJSON
			}
			if _, duplicate := object[key]; duplicate {
				return nil, errReadinessJSON
			}
			value, err := jsonValue(d, depth+1, budget)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') {
			return nil, errReadinessJSON
		}
		return object, nil
	case '[':
		var array []any
		for d.More() {
			value, err := jsonValue(d, depth+1, budget)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if end, err := d.Token(); err != nil || end != json.Delim(']') {
			return nil, errReadinessJSON
		}
		return array, nil
	}
	return nil, errReadinessJSON
}

func jsonField(object map[string]any, field string) (any, bool) {
	var value any = object
	for _, key := range strings.Split(field, ".") {
		parent, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = parent[key]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func fieldsMatch(object map[string]any, checks []profile.ReadinessField) bool {
	for _, check := range checks {
		v, ok := jsonField(object, check.Field)
		if !ok || !fieldMatches(v, check) {
			return false
		}
	}
	return true
}

func fieldMatches(v any, c profile.ReadinessField) bool {
	s, isString := v.(string)
	switch c.Kind {
	case "string_equal":
		return isString && s == c.Value
	case "nonempty_string":
		return isString && strings.TrimSpace(s) != ""
	case "integer_equal", "positive_integer":
		number, ok := v.(json.Number)
		if !ok {
			return false
		}
		n, err := number.Int64()
		return err == nil && (c.Kind == "positive_integer" && n > 0 || c.Kind == "integer_equal" && strconv.FormatInt(n, 10) == c.Value)
	case "boolean_equal":
		b, ok := v.(bool)
		return ok && strconv.FormatBool(b) == c.Value
	case "string_array_contains":
		array, ok := v.([]any)
		found := false
		for _, item := range array {
			text, isString := item.(string)
			if !isString {
				return false
			}
			found = found || text == c.Value
		}
		return ok && found
	case "loopback_http_origin":
		if !isString {
			return false
		}
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
			return false
		}
		ip := net.ParseIP(u.Hostname())
		port, err := strconv.Atoi(u.Port())
		return ip != nil && ip.IsLoopback() && err == nil && port > 0 && port <= 65535
	}
	return false
}

func offlineMatches(object map[string]any, check *profile.OfflineReadiness) bool {
	if !fieldsMatch(object, check.Checks) {
		return false
	}
	for _, member := range check.Members {
		value, ok := jsonField(object, member.Field)
		array, isArray := value.([]any)
		if !ok || !isArray {
			return false
		}
		matches := 0
		for _, item := range array {
			object, ok := item.(map[string]any)
			value, exists := jsonField(object, member.MatchField)
			text, isString := value.(string)
			if ok && exists && isString && text == member.MatchValue {
				if !fieldsMatch(object, member.Checks) {
					return false
				}
				matches++
			}
		}
		if matches != 1 {
			return false
		}
	}
	return true
}
