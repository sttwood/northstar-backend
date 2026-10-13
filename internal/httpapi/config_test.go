package httpapi

import (
	"context"
	"strings"
	"testing"
)

func TestProductionRejectsIncompleteAndInsecureConfiguration(t *testing.T) {
	t.Setenv("PRODUCTION_PROFILE", "full")
	for _, key := range []string{"MAIL_DELIVERY", "FRONTEND_UPSTREAM_URL", "ENCRYPTION_KEY", "SMTP_ADDRESS", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM", "S3_BUCKET", "S3_ENDPOINT", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_APPLICATION_CREDENTIALS", "CLAMAV_ADDRESS", "SEED_DEMO"} {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_PUBLIC_URL", "https://intranet.example.test")
	t.Setenv("API_PUBLIC_URL", "https://intranet.example.test/api")
	t.Setenv("COOKIE_SECURE", "true")
	check := func(fragment string) {
		t.Helper()
		_, err := Configuration(context.Background(), []string{"https://intranet.example.test"}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), fragment) {
			t.Fatalf("expected %s error, got %v", fragment, err)
		}
	}
	check("requires encryption")
	t.Setenv("API_PUBLIC_URL", "https://intranet.example.test/wrong")
	check("HTTPS")
	t.Setenv("API_PUBLIC_URL", "https://intranet.example.test/api")
	t.Setenv("COOKIE_SECURE", "false")
	check("HTTPS")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("ENCRYPTION_KEY", "invalid")
	check("ENCRYPTION_KEY")
	t.Setenv("ENCRYPTION_KEY", "")
	t.Setenv("SMTP_ADDRESS", "invalid")
	check("host:port")
	t.Setenv("SMTP_ADDRESS", "localhost:587")
	check("SMTP_FROM")
	t.Setenv("SMTP_ADDRESS", "")
	t.Setenv("S3_BUCKET", "private")
	t.Setenv("S3_ENDPOINT", "http://storage.example.test")
	check("requires HTTPS")
}
