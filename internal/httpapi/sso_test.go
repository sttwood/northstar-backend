package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"northstar/backend/internal/domain"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

func TestGoogleSSOPKCEClaimsInvitationAndReplay(t *testing.T) {
	a, _, repo, _ := productionHarness(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test-key"}}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	issuer, nonce, challenge := "", "", ""
	email, hd, verified := "maya@northstar.test", "northstar.test", true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			r.ParseForm()
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				t.Error("PKCE binding not verified")
			}
			claims, _ := json.Marshal(map[string]any{"iss": issuer, "aud": "client-id", "sub": "google-subject", "nonce": nonce, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "email": email, "email_verified": verified, "hd": hd, "name": "Google colleague"})
			object, _ := signer.Sign(claims)
			jwt, _ := object.CompactSerialize()
			json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": jwt})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	issuer = server.URL
	provider, err := oidc.NewProvider(context.Background(), issuer)
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.OIDC = provider
	a.cfg.OAuth = oauth2.Config{ClientID: "client-id", ClientSecret: "client-secret", Endpoint: provider.Endpoint(), RedirectURL: "http://localhost:8080/api/auth/sso/callback", Scopes: []string{"openid", "email", "profile"}}
	err = repo.Update(domain.WithTenant(context.Background(), "default"), func(s *domain.State) error { s.Company("default").Domain = "northstar.test"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	start := func() (url.Values, *http.Cookie) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://localhost:8080/api/auth/sso/google?company=northstar", nil)
		if err := a.ssoStart(w, r); err != nil {
			t.Fatal(err)
		}
		if w.Code != 303 {
			t.Fatalf("start failed: %d", w.Code)
		}
		location, _ := url.Parse(w.Header().Get("Location"))
		values := location.Query()
		nonce = values.Get("nonce")
		challenge = values.Get("code_challenge")
		if values.Get("code_challenge_method") != "S256" || values.Get("hd") != "northstar.test" {
			t.Fatal("Google request missing PKCE/domain")
		}
		return values, w.Result().Cookies()[0]
	}
	callback := func(values url.Values, cookie *http.Cookie) (*httptest.ResponseRecorder, error) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://localhost:8080/api/auth/sso/callback?state="+url.QueryEscape(values.Get("state"))+"&code=one-use", nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		err := a.ssoCallback(w, r)
		return w, err
	}
	values, cookie := start()
	if _, err = callback(values, nil); err == nil {
		t.Fatal("missing state cookie accepted")
	}
	w, err := callback(values, cookie)
	if err != nil || w.Code != 303 {
		t.Fatalf("verified SSO failed: %v", err)
	}
	sessionFound := false
	for _, c := range w.Result().Cookies() {
		sessionFound = sessionFound || c.Name == "northstar_session" && c.Value != ""
	}
	if !sessionFound {
		t.Fatal("SSO did not create session")
	}
	if _, err = callback(values, cookie); err == nil {
		t.Fatal("SSO state replay accepted")
	}
	for _, scenario := range []string{"domain", "unverified", "nonce", "uninvited"} {
		t.Run(scenario, func(t *testing.T) {
			email = "maya@northstar.test"
			hd = "northstar.test"
			verified = true
			values, cookie := start()
			switch scenario {
			case "domain":
				hd = "attacker.test"
			case "unverified":
				verified = false
			case "nonce":
				nonce = "incorrect"
			case "uninvited":
				email = "stranger@northstar.test"
				repo.Update(domain.WithTenant(context.Background(), "default"), func(s *domain.State) error {
					for i := range s.Security {
						s.Security[i].GoogleSub = ""
					}
					return nil
				})
			}
			if _, err = callback(values, cookie); err == nil {
				t.Fatal("invalid SSO identity accepted")
			}
		})
	}
	state, _ := repo.Read(domain.WithTenant(context.Background(), "default"))
	for _, e := range state.Employees {
		if strings.HasPrefix(e.Email, "stranger@") {
			t.Fatal("uninvited account provisioned")
		}
	}
}
