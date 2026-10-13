package domain

import (
	"encoding/json"
	"testing"
)

func TestEnvironmentValidation(t *testing.T) {
	tests := []struct {
		name, input string
		invalid     bool
	}{
		{"valid references", `{"dev":{"status":"healthy","url":"http://localhost:3000","docs":[{"id":"d1","title":"คู่มือ","url":"https://example.com/docs"}],"accounts":[{"id":"a1","name":"Reviewer","username":"qa@example.com","role":"Tester","url":"https://example.com/login","secretRef":"https://example.com/vault/1","status":"active"}]}}`, false},
		{"executable link", `{"dev":{"status":"healthy","url":"javascript:alert(1)"}}`, true},
		{"embedded credentials", `{"dev":{"status":"healthy","url":"https://user:pass@example.com"}}`, true},
		{"password field", `{"dev":{"status":"healthy","accounts":[{"id":"a","name":"Test","username":"test","role":"Tester","status":"active","password":"secret"}]}}`, true},
		{"unknown environment", `{"staging":{"status":"healthy"}}`, true},
		{"null environment", `{"dev":null}`, true},
		{"duplicate IDs", `{"dev":{"status":"healthy","docs":[{"id":"x","title":"One","url":"https://example.com"},{"id":"x","title":"Two","url":"https://example.com"}]}}`, true},
		{"invalid event date", `{"prod":{"status":"healthy","timeline":[{"id":"e","title":"Release","date":"2026-02-30","status":"planned"}]}}`, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var input any
			if err := json.Unmarshal([]byte(test.input), &input); err != nil {
				t.Fatal(err)
			}
			err := validateEnvironments(input)
			if (err != nil) != test.invalid {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}
