package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"northstar/backend/internal/bootstrap"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/store"
	"strings"
	"testing"
)

func coreEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"FRONTEND_UPSTREAM_URL", "SMTP_ADDRESS", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM", "S3_BUCKET", "S3_ENDPOINT", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_APPLICATION_CREDENTIALS", "CLAMAV_ADDRESS", "SEED_DEMO"} {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("PRODUCTION_PROFILE", "core")
	t.Setenv("UPLOAD_POLICY", "scanned")
	t.Setenv("MAIL_DELIVERY", "disabled")
	t.Setenv("APP_PUBLIC_URL", "https://intranet.example.test")
	t.Setenv("API_PUBLIC_URL", "https://intranet.example.test/api")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
}
func TestCoreProductionIsRealAndDisablesUnconfiguredProviders(t *testing.T) {
	coreEnvironment(t)
	cfg, err := Configuration(context.Background(), []string{"https://intranet.example.test"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Production || cfg.ProductionProfile != "core" || !cfg.DisableUploads || !cfg.SecureCookie || len(cfg.MasterKey) != 32 || cfg.Mailer != nil || cfg.OIDC != nil || cfg.Drive != nil {
		t.Fatal("unsafe core configuration")
	}
	s := domain.State{}
	if err = bootstrap.Owner(&s, "owner@example.test", "SyntheticOwnerSecret2026!", "Superadmin", "Nexilar", "nexilar"); err != nil {
		t.Fatal(err)
	}
	h := New(store.NewMemory(s), cfg)
	w := call(t, h, "POST", "/api/auth/login", map[string]string{"company": "nexilar", "email": "owner@example.test", "password": "SyntheticOwnerSecret2026!"}, nil)
	assertCode(t, w, 200)
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/api" {
		t.Fatal("insecure production cookie")
	}
	assertCode(t, call(t, h, "GET", "/api/v2/bootstrap", nil, cookie), 200)
	assertCode(t, call(t, h, "POST", "/api/files", nil, cookie), 503)
	w = call(t, h, "GET", "/api/company", nil, cookie)
	assertCode(t, w, 200)
	var status struct {
		Integrations map[string]any `json:"integrations"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	if status.Integrations["fileUploads"] != false || status.Integrations["storage"] != "disabled" || status.Integrations["productionProfile"] != "core" {
		t.Fatal("deferred features misreported")
	}
	assertCode(t, call(t, h, "POST", "/api/integrations/drive/sync", nil, cookie), 503)
	t.Setenv("PRODUCTION_PROFILE", "full")
	_, err = Configuration(context.Background(), []string{"https://intranet.example.test"}, t.TempDir())
	if err == nil {
		t.Fatal("strict full production was weakened")
	}
}
func TestCoreProductionRetainsStartupRequirements(t *testing.T) {
	coreEnvironment(t)
	check := func(fragment string) {
		t.Helper()
		_, err := Configuration(context.Background(), []string{"https://intranet.example.test"}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), fragment) {
			t.Fatalf("expected %s, got %v", fragment, err)
		}
	}
	t.Setenv("PRODUCTION_PROFILE", "typo")
	check("PRODUCTION_PROFILE")
	t.Setenv("PRODUCTION_PROFILE", "core")
	t.Setenv("ENCRYPTION_KEY", "")
	check("requires encryption")
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("COOKIE_SECURE", "false")
	check("HTTPS")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("MAIL_DELIVERY", "preview")
	check("preview email")
	t.Setenv("MAIL_DELIVERY", "disabled")
	t.Setenv("SEED_DEMO", "true")
	check("demo seeds")
}
