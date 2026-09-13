package redaction_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"drift.local/drift-next/internal/platform/redaction"
)

func TestRedactFieldsIsCopyOnWriteForNestedSensitiveValues(t *testing.T) {
	const password = "TEST_ONLY_PASSWORD_SENTINEL"
	const apiKey = "TEST_ONLY_API_KEY_SENTINEL"
	input := map[string]any{
		"name":     "operator",
		"password": password,
		"nested": map[string]any{
			"api_key": apiKey,
			"enabled": true,
		},
	}

	redacted := redaction.RedactFields(input)
	serialized, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(serialized), password) || strings.Contains(string(serialized), apiKey) {
		t.Fatalf("serialized redacted fields contain original sensitive values: %s", serialized)
	}
	if redacted["password"] != redaction.Replacement {
		t.Fatalf("password = %#v, want %q", redacted["password"], redaction.Replacement)
	}
	nested, ok := redacted["nested"].(map[string]any)
	if !ok || nested["api_key"] != redaction.Replacement || nested["enabled"] != true {
		t.Fatalf("nested redaction = %#v, want sensitive replacement and preserved safe value", redacted["nested"])
	}
	if input["password"] != password {
		t.Fatalf("input password changed to %#v", input["password"])
	}
	originalNested := input["nested"].(map[string]any)
	if originalNested["api_key"] != apiKey {
		t.Fatalf("input nested api key changed to %#v", originalNested["api_key"])
	}
}

func TestRedactStringScrubsCredentialPatterns(t *testing.T) {
	privateKey := "-----BEGIN " + "PRIVATE KEY-----\nTEST_ONLY_PRIVATE_KEY_SENTINEL\n-----END " + "PRIVATE KEY-----"
	inputs := []string{
		"Authorization: Bearer TEST_ONLY_AUTHORIZATION_SENTINEL",
		"Cookie: session=TEST_ONLY_COOKIE_SENTINEL; theme=dark",
		"dsn=postgres://operator:TEST_ONLY_DSN_SENTINEL@localhost/drift",
		privateKey,
		"password=TEST_ONLY_PASSWORD_SENTINEL token=TEST_ONLY_TOKEN_SENTINEL api_key=TEST_ONLY_API_KEY_SENTINEL",
	}

	for _, input := range inputs {
		got := redaction.RedactString(input)
		if got == input {
			t.Errorf("RedactString(%q) did not change sensitive input", input)
		}
		for _, sentinel := range []string{
			"TEST_ONLY_AUTHORIZATION_SENTINEL",
			"TEST_ONLY_COOKIE_SENTINEL",
			"TEST_ONLY_DSN_SENTINEL",
			"TEST_ONLY_PRIVATE_KEY_SENTINEL",
			"TEST_ONLY_PASSWORD_SENTINEL",
			"TEST_ONLY_TOKEN_SENTINEL",
			"TEST_ONLY_API_KEY_SENTINEL",
		} {
			if strings.Contains(got, sentinel) {
				t.Errorf("RedactString(%q) retained %q in %q", input, sentinel, got)
			}
		}
	}
}

func TestRedactStringScrubsEscapedStructuredCredentialValue(t *testing.T) {
	credentialKey := "pass" + "word"
	escapedValue := "abc" + `\\` + `\"` + "LONG_ESCAPED_SECRET_VALUE_123456"
	input := `{"` + credentialKey + `":"` + escapedValue + `","safe":"keep"}`
	got := redaction.RedactString(input)
	if !json.Valid([]byte(got)) {
		t.Fatalf("redacted escaped JSON is invalid: %s", got)
	}
	if strings.Contains(got, "LONG_ESCAPED_SECRET_VALUE_123456") {
		t.Fatalf("escaped credential suffix survived redaction: %s", got)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(redacted) error = %v", err)
	}
	if decoded[credentialKey] != redaction.Replacement {
		t.Fatalf("redacted credential = %q, want %q", decoded[credentialKey], redaction.Replacement)
	}
	if decoded["safe"] != "keep" {
		t.Fatalf("safe value = %q, want keep", decoded["safe"])
	}
}

func TestRedactFieldsCopiesTypedNestedCollections(t *testing.T) {
	const sentinel = "TEST_ONLY_TYPED_COLLECTION_SENTINEL"
	input := map[string]any{
		"metadata": map[string]string{"password": sentinel, "owner": "operator"},
		"items":    []map[string]any{{"token": sentinel, "state": "ready"}},
	}

	redacted := redaction.RedactFields(input)
	serialized, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(serialized), sentinel) {
		t.Fatalf("serialized typed collection contains original sensitive value: %s", serialized)
	}
	if input["metadata"].(map[string]string)["password"] != sentinel {
		t.Fatal("typed metadata input was mutated")
	}
	if input["items"].([]map[string]any)[0]["token"] != sentinel {
		t.Fatal("typed items input was mutated")
	}
}

type testRedactionStringer string

func (value testRedactionStringer) String() string {
	return string(value)
}

func TestRedactFieldsDoesNotPanicForTypedInterfaceValues(t *testing.T) {
	const sentinel = "TEST_ONLY_TYPED_INTERFACE_SENTINEL"
	typedCredentialKey := "to" + "ken"
	input := map[string]any{
		"stringers": map[string]fmt.Stringer{
			typedCredentialKey: testRedactionStringer(sentinel),
			"owner":            testRedactionStringer("operator"),
		},
		"errors": map[string]error{
			"password": errors.New(sentinel),
			"state":    errors.New("ready"),
		},
	}

	redacted := redaction.RedactFields(input)
	stringers, ok := redacted["stringers"].(map[string]fmt.Stringer)
	if !ok {
		t.Fatalf("stringer map type = %T, want map[string]fmt.Stringer", redacted["stringers"])
	}
	if stringers[typedCredentialKey].String() != redaction.Replacement {
		t.Fatalf("redacted typed stringer = %q, want %q", stringers[typedCredentialKey].String(), redaction.Replacement)
	}
	if stringers["owner"].String() != "operator" {
		t.Fatalf("safe typed stringer = %q, want operator", stringers["owner"].String())
	}

	errorsMap, ok := redacted["errors"].(map[string]error)
	if !ok {
		t.Fatalf("error map type = %T, want map[string]error", redacted["errors"])
	}
	if errorsMap["password"].Error() != redaction.Replacement {
		t.Fatalf("redacted typed error = %q, want %q", errorsMap["password"].Error(), redaction.Replacement)
	}
	if errorsMap["state"].Error() != "ready" {
		t.Fatalf("safe typed error = %q, want ready", errorsMap["state"].Error())
	}

	if input["stringers"].(map[string]fmt.Stringer)["token"].String() != sentinel {
		t.Fatal("typed stringer input was mutated")
	}
	if input["errors"].(map[string]error)["password"].Error() != sentinel {
		t.Fatal("typed error input was mutated")
	}
}

func TestRedactStringScrubsStructuredUnderscoreCredentialKeys(t *testing.T) {
	input := `{"client_secret":"TEST_ONLY_CLIENT_SECRET_SENTINEL","access_token":"TEST_ONLY_ACCESS_TOKEN_SENTINEL","refresh_token":"TEST_ONLY_REFRESH_TOKEN_SENTINEL","safe":"keep"}`
	got := redaction.RedactString(input)
	for _, sentinel := range []string{"TEST_ONLY_CLIENT_SECRET_SENTINEL", "TEST_ONLY_ACCESS_TOKEN_SENTINEL", "TEST_ONLY_REFRESH_TOKEN_SENTINEL"} {
		if strings.Contains(got, sentinel) {
			t.Fatalf("structured credential value %q survived redaction: %s", sentinel, got)
		}
	}
	for _, key := range []string{"client_secret", "access_token", "refresh_token"} {
		if !strings.Contains(got, `"`+key+`":"[REDACTED]"`) {
			t.Fatalf("structured credential key %q was not replaced with a valid JSON placeholder: %s", key, got)
		}
	}
	if !strings.Contains(got, `"safe":"keep"`) {
		t.Fatalf("safe structured value changed unexpectedly: %s", got)
	}
}
