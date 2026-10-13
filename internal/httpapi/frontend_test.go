package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/store"
	"strings"
	"testing"
)

func TestBackendFrontendEntryPoint(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.RequestURI() != "/signin?next=%2Fprofile" || r.Host != "intranet.test" || r.Header.Get("X-Forwarded-Host") != "intranet.test" || strings.Contains(r.Header.Get("X-Forwarded-For"), "spoofed") {
			t.Errorf("invalid proxy routing or forwarded headers")
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<main>Sign in</main>")
	}))
	defer upstream.Close()
	h := New(store.NewMemory(domain.State{}), Config{FrontendURL: upstream.URL, UploadDir: t.TempDir()})
	r := httptest.NewRequest("GET", "http://intranet.test/signin?next=%2Fprofile", nil)
	r.Header.Set("X-Forwarded-For", "spoofed")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Sign in") {
		t.Fatalf("frontend response: %d %s", w.Code, w.Body)
	}
	assertCode(t, call(t, h, "GET", "/api/auth/providers", nil, nil), 200)
	assertCode(t, call(t, h, "GET", "/api/unknown", nil, nil), 404)
	assertCode(t, call(t, h, "GET", "/api", nil, nil), 404)
	assertCode(t, call(t, h, "GET", "/healthz", nil, nil), 200)
	if requests != 1 {
		t.Fatalf("API or health requests reached frontend: %d", requests)
	}
	upstream.Close()
	assertCode(t, call(t, h, "GET", "/signin", nil, nil), 503)
}

func TestDevelopmentDeliveryAndProxyConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("ENCRYPTION_KEY", "")
	t.Setenv("API_PUBLIC_URL", "http://localhost:8080/api")
	for _, key := range []string{"GOOGLE_CLIENT_ID", "GOOGLE_APPLICATION_CREDENTIALS", "S3_BUCKET", "SMTP_ADDRESS", "CLAMAV_ADDRESS", "FRONTEND_UPSTREAM_URL"} {
		t.Setenv(key, "")
	}
	t.Setenv("MAIL_DELIVERY", "preview")
	cfg, err := Configuration(t.Context(), nil, t.TempDir())
	if err != nil || cfg.Mailer == nil || cfg.MailMode != "preview" {
		t.Fatalf("preview config: %v", err)
	}
	t.Setenv("APP_ENV", "production")
	if _, err = Configuration(t.Context(), nil, t.TempDir()); err == nil || !strings.Contains(err.Error(), "preview") {
		t.Fatalf("production preview accepted: %v", err)
	}
	t.Setenv("APP_ENV", "development")
	t.Setenv("MAIL_DELIVERY", "disabled")
	for _, origin := range []string{"file:///tmp", "http://localhost:3000/path", "http://localhost:8080", "http://user:pass@localhost:3000"} {
		t.Setenv("FRONTEND_UPSTREAM_URL", origin)
		if _, err = Configuration(t.Context(), nil, t.TempDir()); err == nil {
			t.Fatalf("invalid upstream accepted: %s", origin)
		}
	}
	t.Setenv("FRONTEND_UPSTREAM_URL", "http://127.0.0.1:3000")
	if _, err = Configuration(t.Context(), nil, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAIL_DELIVERY", "smtp")
	if _, err = Configuration(t.Context(), nil, t.TempDir()); err == nil {
		t.Fatal("SMTP selected without SMTP_ADDRESS")
	}
}
