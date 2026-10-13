package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"net"
	"net/mail"
	"net/url"
	"northstar/backend/internal/integrations"
	"os"
	"strings"
	"time"
)

func Configuration(ctx context.Context, origins []string, uploadDir string) (Config, error) {
	cfg := Config{Origins: origins, UploadDir: uploadDir, SecureCookie: os.Getenv("COOKIE_SECURE") == "true", Production: os.Getenv("APP_ENV") == "production", AppURL: strings.TrimRight(os.Getenv("APP_PUBLIC_URL"), "/"), APIURL: strings.TrimRight(os.Getenv("API_PUBLIC_URL"), "/")}
	if cfg.AppURL == "" {
		// Public URLs below are completed for native local development.
		cfg.AppURL = "http://localhost:3000"
	}
	cfg.ProductionProfile = "development"
	cfg.UploadPolicy = os.Getenv("UPLOAD_POLICY")
	if cfg.UploadPolicy == "" {
		cfg.UploadPolicy = "scanned"
	}
	if cfg.UploadPolicy != "scanned" && cfg.UploadPolicy != "images-only" {
		return cfg, errors.New("UPLOAD_POLICY must be scanned or images-only")
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "http://localhost:8080/api"
	}
	cfg.FrontendURL = strings.TrimRight(os.Getenv("FRONTEND_UPSTREAM_URL"), "/")
	if cfg.FrontendURL != "" {
		upstream, err := url.Parse(cfg.FrontendURL)
		public, publicErr := url.Parse(cfg.APIURL)
		if err != nil || publicErr != nil || upstream.Host == "" || upstream.User != nil || upstream.Path != "" || upstream.RawQuery != "" || upstream.Fragment != "" || (upstream.Scheme != "http" && upstream.Scheme != "https") || strings.EqualFold(upstream.Host, public.Host) {
			return cfg, errors.New("FRONTEND_UPSTREAM_URL must be a separate HTTP(S) origin without a path")
		}
	}
	cfg.MailMode = "disabled"
	mailMode := os.Getenv("MAIL_DELIVERY")
	if mailMode == "smtp" && os.Getenv("SMTP_ADDRESS") == "" {
		return cfg, errors.New("MAIL_DELIVERY=smtp requires SMTP_ADDRESS")
	}
	if mailMode != "" && mailMode != "smtp" && mailMode != "preview" && mailMode != "disabled" {
		return cfg, errors.New("MAIL_DELIVERY must be smtp, preview or disabled")
	}
	if mailMode == "preview" {
		if cfg.Production {
			return cfg, errors.New("production cannot use preview email delivery")
		}
		dir := os.Getenv("PREVIEW_EMAIL_DIR")
		if dir == "" {
			dir = "./.local/mail"
		}
		cfg.Mailer = integrations.PreviewMail{Dir: dir}
		cfg.MailMode = "preview"
	}
	if key := os.Getenv("ENCRYPTION_KEY"); key != "" {
		b, err := base64.StdEncoding.DecodeString(key)
		if err != nil || len(b) != 32 {
			return cfg, errors.New("ENCRYPTION_KEY must contain exactly 32 base64-encoded bytes")
		}
		cfg.MasterKey = b
	}
	if address := os.Getenv("SMTP_ADDRESS"); address != "" && (mailMode == "" || mailMode == "smtp") {
		if _, _, err := net.SplitHostPort(address); err != nil {
			return cfg, errors.New("SMTP_ADDRESS must be host:port")
		}
		if _, err := mail.ParseAddress(os.Getenv("SMTP_FROM")); err != nil {
			return cfg, errors.New("SMTP_FROM must be a valid sender address")
		}
		if (os.Getenv("SMTP_USERNAME") == "") != (os.Getenv("SMTP_PASSWORD") == "") {
			return cfg, errors.New("SMTP_USERNAME and SMTP_PASSWORD must be configured together")
		}
		cfg.Mailer = integrations.SMTP{Address: address, Username: os.Getenv("SMTP_USERNAME"), Password: os.Getenv("SMTP_PASSWORD"), From: os.Getenv("SMTP_FROM"), ImplicitTLS: os.Getenv("SMTP_IMPLICIT_TLS") == "true"}
		cfg.MailMode = "smtp"
	}
	if address := os.Getenv("CLAMAV_ADDRESS"); address != "" {
		cfg.Scanner = integrations.ClamAV{Address: address}
	}
	if bucket := os.Getenv("S3_BUCKET"); bucket != "" {
		if endpoint := os.Getenv("S3_ENDPOINT"); endpoint != "" {
			parsed, err := url.Parse(endpoint)
			if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && (cfg.Production || parsed.Scheme != "http")) {
				return cfg, errors.New("S3_ENDPOINT must be a valid URL; production requires HTTPS")
			}
		}
		connect, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		blobs, err := integrations.NewS3(connect, bucket, os.Getenv("AWS_REGION"), os.Getenv("S3_ENDPOINT"))
		if err != nil {
			return cfg, errors.New("S3 storage configuration could not be verified")
		}
		cfg.Blobs = blobs
	}
	if credentials := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); credentials != "" {
		b, err := os.ReadFile(credentials)
		if err != nil {
			return cfg, errors.New("Google service-account file could not be read")
		}
		drive, err := integrations.NewDrive(ctx, b)
		if err != nil {
			return cfg, errors.New("Google Drive service-account configuration is invalid")
		}
		cfg.Drive = drive
	}
	if clientID := os.Getenv("GOOGLE_CLIENT_ID"); clientID != "" {
		if os.Getenv("GOOGLE_CLIENT_SECRET") == "" {
			return cfg, errors.New("GOOGLE_CLIENT_SECRET is required")
		}
		connect, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		provider, err := oidc.NewProvider(connect, "https://accounts.google.com")
		if err != nil {
			return cfg, errors.New("Google identity discovery failed")
		}
		cfg.OIDC = provider
		cfg.OAuth = oauth2.Config{ClientID: clientID, ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"), Endpoint: provider.Endpoint(), RedirectURL: cfg.APIURL + "/auth/sso/callback", Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}
	}
	if cfg.Production {
		cfg.ProductionProfile = os.Getenv("PRODUCTION_PROFILE")
		if cfg.ProductionProfile == "" {
			cfg.ProductionProfile = "full"
		}
		if cfg.ProductionProfile != "full" && cfg.ProductionProfile != "core" {
			return cfg, errors.New("PRODUCTION_PROFILE must be full or core")
		}
		if cfg.UploadPolicy == "images-only" && cfg.ProductionProfile != "core" {
			return cfg, errors.New("images-only uploads require PRODUCTION_PROFILE=core")
		}
		app, e1 := url.Parse(cfg.AppURL)
		api, e2 := url.Parse(cfg.APIURL)
		if e1 != nil || e2 != nil || app.Scheme != "https" || api.Scheme != "https" || app.Host == "" || api.Host == "" || app.User != nil || api.User != nil || app.RawQuery != "" || api.RawQuery != "" || app.Fragment != "" || api.Fragment != "" || (app.Path != "" && app.Path != "/") || api.Path != "/api" || !cfg.SecureCookie {
			return cfg, errors.New("production requires HTTPS public URLs and COOKIE_SECURE=true")
		}
		if os.Getenv("SEED_DEMO") == "true" {
			return cfg, errors.New("production cannot use demo seeds")
		}
		if len(cfg.MasterKey) != 32 {
			return cfg, errors.New("production requires encryption: configure ENCRYPTION_KEY")
		}
		if cfg.ProductionProfile == "full" && (cfg.Mailer == nil || cfg.Scanner == nil || cfg.Blobs == nil || cfg.OIDC == nil || cfg.Drive == nil) {
			return cfg, errors.New("production requires encryption, SMTP, ClamAV, S3, Google SSO and Drive configuration")
		}
		cfg.DisableUploads = cfg.Blobs == nil || cfg.Scanner == nil || cfg.UploadPolicy == "images-only"
		cfg.AllowImageUploads = cfg.Blobs != nil && cfg.UploadPolicy == "images-only"
		if !containsOrigin(origins, app.Scheme+"://"+app.Host) {
			return cfg, errors.New("ALLOWED_ORIGINS must include APP_PUBLIC_URL origin")
		}
	}
	return cfg, nil
}
func containsOrigin(origins []string, value string) bool {
	for _, origin := range origins {
		if origin == value {
			return true
		}
	}
	return false
}
