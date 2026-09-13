package redaction_test

import (
	"encoding/json"
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
