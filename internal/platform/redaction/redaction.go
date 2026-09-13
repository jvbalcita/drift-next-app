// Package redaction provides explicit, copy-on-write scrubbing at persistence
// and logging boundaries.
package redaction

import (
	"reflect"
	"regexp"
	"strings"
)

// Replacement is the only value emitted for a sensitive field.
const Replacement = "[REDACTED]"

var (
	privateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	bearerPattern     = regexp.MustCompile(`(?i)(\bauthorization\s*[:=]\s*(?:bearer|basic)\s+)[^\s,;]+`)
	cookiePattern     = regexp.MustCompile(`(?i)(\bcookie\s*:\s*)[^\r\n]+`)
	dsnPattern        = regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://[^/\s:@]+:)[^@\s/]+(@)`)
	credentialPattern = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_])(["']?(?:[a-z0-9]+[_-])*(?:password|passwd|passphrase|token|api[_-]?key|apikey|authorization|cookie|private[_-]?key|dsn|connection[_-]?string|secret|credential)(?:[_-][a-z0-9]+)*["']?)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^,\s;&}\]]+)`)
)

// RedactString removes recognized credential material from a free-form
// string, including headers, connection strings, and PEM private-key blocks.
func RedactString(input string) string {
	redacted := privateKeyPattern.ReplaceAllString(input, Replacement)
	redacted = bearerPattern.ReplaceAllString(redacted, `$1`+Replacement)
	redacted = cookiePattern.ReplaceAllString(redacted, `$1`+Replacement)
	redacted = credentialPattern.ReplaceAllStringFunc(redacted, func(match string) string {
		parts := credentialPattern.FindStringSubmatch(match)
		value := parts[4]
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[:1] + Replacement + value[len(value)-1:]
		} else {
			value = Replacement
		}
		return parts[1] + parts[2] + parts[3] + value
	})
	return dsnPattern.ReplaceAllString(redacted, `$1`+Replacement+`$2`)
}

// RedactFields returns a deep copy of fields with recognized sensitive map
// fields replaced. The input map and all nested maps/slices remain unchanged.
func RedactFields(fields map[string]any) map[string]any {
	if fields == nil {
		return nil
	}
	redacted := make(map[string]any, len(fields))
	for key, value := range fields {
		if sensitiveKey(key) {
			redacted[key] = Replacement
			continue
		}
		redacted[key] = copyValue(value)
	}
	return redacted
}

func copyValue(value any) any {
	if value == nil {
		return nil
	}
	return copyReflect(reflect.ValueOf(value)).Interface()
}

func copyReflect(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copied := copyReflect(value.Elem())
		result := reflect.New(value.Type()).Elem()
		result.Set(copied)
		return result
	case reflect.String:
		redacted := reflect.New(value.Type()).Elem()
		redacted.SetString(RedactString(value.String()))
		return redacted
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key := iter.Key()
			if key.Kind() == reflect.String && sensitiveKey(key.String()) {
				result.SetMapIndex(key, replacementFor(value.Type().Elem()))
				continue
			}
			result.SetMapIndex(copyReflect(key), copyReflect(iter.Value()))
		}
		return result
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			result.Index(index).Set(copyReflect(value.Index(index)))
		}
		return result
	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			result.Index(index).Set(copyReflect(value.Index(index)))
		}
		return result
	default:
		return value
	}
}

func replacementFor(valueType reflect.Type) reflect.Value {
	if valueType.Kind() == reflect.Interface {
		result := reflect.New(valueType).Elem()
		result.Set(reflect.ValueOf(Replacement))
		return result
	}
	if valueType.Kind() == reflect.String {
		result := reflect.New(valueType).Elem()
		result.SetString(Replacement)
		return result
	}
	if valueType.Kind() == reflect.Slice && valueType.Elem().Kind() == reflect.Uint8 {
		result := reflect.New(valueType).Elem()
		result.SetBytes([]byte(Replacement))
		return result
	}
	return reflect.Zero(valueType)
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(key))
	for _, fragment := range []string{
		"password",
		"passphrase",
		"token",
		"api_key",
		"apikey",
		"authorization",
		"cookie",
		"private_key",
		"privatekey",
		"dsn",
		"connection_string",
		"connectionstring",
		"secret",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}
