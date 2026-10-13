package httpapi

import (
	"context"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"net/http"
	"net/mail"
	"net/url"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/store"
	"strings"
	"time"
)

func (a *API) limited(key string, maximum int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	recent := []time.Time{}
	for _, t := range a.attempts[key] {
		if now.Sub(t) < 15*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(a.attempts) > 20000 {
		a.attempts = map[string][]time.Time{}
	}
	a.attempts[key] = append(recent, now)
	return len(recent) >= maximum
}
func (a *API) startSession(s *domain.State, u domain.Employee, raw, assurance string) error {
	active := s.Employee(u.ID)
	if active == nil || !domain.Active(*active) {
		return fail(401, "account is inactive")
	}
	s.Sessions = append(s.Sessions, domain.Session{Hash: tokenHash(raw), EmployeeID: u.ID, Expires: time.Now().Add(12 * time.Hour), Assurance: assurance})
	audit(s, u, "signed in", u.ID)
	return nil
}
func (a *API) finishLogin(w http.ResponseWriter, r *http.Request, u domain.Employee, assurance string, redirect bool) {
	ctx := domain.WithTenant(r.Context(), u.CompanyID)
	raw := id() + id()
	challenge := ""
	setup := false
	err := a.store.Update(ctx, func(s *domain.State) error {
		c := s.Company(u.CompanyID)
		if c == nil {
			return fail(403, "company not found")
		}
		account := s.Account(u.ID)
		if account == nil {
			return fail(401, "account is inactive")
		}
		if account.Enabled || c.RequireMFA {
			if len(a.cfg.MasterKey) != 32 {
				return fail(503, "MFA encryption is not configured")
			}
			challenge = id() + id()
			setup = !account.Enabled
			s.Operations = append(s.Operations, domain.Operation{ID: tokenHash(challenge), CompanyID: domain.CompanyID(u.CompanyID), Kind: "mfa-challenge", EmployeeID: u.ID, Status: "pending", CreatedAt: stamp(), ExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano), Data: map[string]any{"setup": setup, "assurance": assurance}})
			return nil
		}
		return a.startSession(s, u, raw, assurance)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	if challenge != "" {
		if redirect {
			http.SetCookie(w, &http.Cookie{Name: "northstar_mfa", Value: challenge, Path: "/api", HttpOnly: true, Secure: a.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 300})
			target := a.cfg.AppURL + "/signin?mfa=1"
			if setup {
				target += "&setup=1"
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
		} else {
			respond(w, 200, map[string]any{"mfaRequired": true, "setupRequired": setup, "challenge": challenge})
		}
		return
	}
	a.cookie(w, raw, 12*3600)
	if redirect {
		http.Redirect(w, r, a.cfg.AppURL, http.StatusSeeOther)
	} else {
		respond(w, 200, u)
	}
}
func (a *API) operation(ctx context.Context, id, kind string) (domain.Operation, error) {
	if lookup, ok := a.store.(interface {
		Operation(context.Context, string) (domain.Operation, error)
	}); ok {
		op, err := lookup.Operation(ctx, id)
		if err != nil || op.Kind != kind {
			return op, fail(400, "link or challenge is invalid or expired")
		}
		return op, nil
	}
	s, err := a.store.Read(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	for _, op := range s.Operations {
		if op.ID == id && op.Kind == kind {
			return op, nil
		}
	}
	return domain.Operation{}, fail(400, "link or challenge is invalid or expired")
}
func validOperation(op domain.Operation) bool {
	expires, err := time.Parse(time.RFC3339Nano, op.ExpiresAt)
	return op.Status == "pending" && err == nil && expires.After(time.Now())
}
func (a *API) company(ctx context.Context, slug string) (domain.Company, error) {
	if lookup, ok := a.store.(interface {
		Company(context.Context, string) (domain.Company, error)
	}); ok {
		return lookup.Company(ctx, slug)
	}
	s, err := a.store.Read(ctx)
	if err != nil {
		return domain.Company{}, err
	}
	for _, c := range s.Companies {
		if c.Slug == slug {
			return c, nil
		}
	}
	return domain.Company{}, fail(404, "company not found")
}
func opText(op domain.Operation, key string) string { v, _ := op.Data[key].(string); return v }
func (a *API) providers(w http.ResponseWriter, r *http.Request) error {
	respond(w, 200, map[string]any{"google": a.cfg.OIDC != nil, "mfa": len(a.cfg.MasterKey) == 32, "signup": "invitation"})
	return nil
}
func (a *API) invite(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if !domain.Manager(u) {
		return fail(403, "manager permission required")
	}
	var body struct {
		Email     string `json:"email"`
		Name      string `json:"name"`
		Role      string `json:"role"`
		Team      string `json:"team"`
		Title     string `json:"title"`
		ManagerID string `json:"managerId"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	parsed, err := mail.ParseAddress(body.Email)
	if err != nil || parsed.Address != body.Email || len(body.Name) > 200 {
		return fail(400, "valid employee email required")
	}
	if body.Role == "" {
		body.Role = "employee"
	}
	if !domain.Contains([]string{"employee", "hr", "owner"}, body.Role) || body.Role == "owner" && u.Role != "owner" {
		return fail(403, "invalid role assignment")
	}
	raw := id() + id()
	op := domain.Operation{ID: tokenHash(raw), CompanyID: domain.CompanyID(u.CompanyID), Kind: "invitation", Status: "pending", CreatedAt: stamp(), ExpiresAt: time.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339Nano), Data: map[string]any{"email": body.Email, "name": body.Name, "role": body.Role, "team": body.Team, "title": body.Title, "managerId": body.ManagerID}}
	var link string
	err = a.store.Update(r.Context(), func(s *domain.State) error {
		for _, e := range s.Employees {
			if e.Email == body.Email {
				return fail(409, "email already exists")
			}
		}
		if err := domain.ValidateManager(s, "", body.ManagerID); err != nil {
			return fail(400, err.Error())
		}
		for i := range s.Operations {
			v := &s.Operations[i]
			if v.Kind == "invitation" && opText(*v, "email") == body.Email && v.Status == "pending" {
				v.Status = "revoked"
			}
		}
		c := s.Company(u.CompanyID)
		link = strings.TrimRight(a.cfg.AppURL, "/") + "/signup?token=" + url.QueryEscape(raw) + "&company=" + url.QueryEscape(c.Slug)
		s.Operations = append(s.Operations, op)
		if a.cfg.Mailer != nil {
			if err := a.queueMail(s, body.Email, "Your invitation to "+c.Name, "You have been invited to "+c.Name+".\n\nCreate your account: "+link+"\n\nThis link expires in seven days."); err != nil {
				return err
			}
		}
		audit(s, u, "invited employee", op.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 201, map[string]any{"id": op.ID, "email": body.Email, "expiresAt": op.ExpiresAt, "inviteUrl": link, "emailQueued": a.cfg.Mailer != nil, "deliveryMode": a.cfg.MailMode})
	return nil
}
func invitedEmployee(op domain.Operation, name, passwordHash string) domain.Employee {
	if strings.TrimSpace(name) == "" {
		name = opText(op, "name")
	}
	return domain.Employee{ID: id(), CompanyID: op.CompanyID, Name: strings.TrimSpace(name), Email: opText(op, "email"), Role: opText(op, "role"), Team: opText(op, "team"), Title: opText(op, "title"), ManagerID: opText(op, "managerId"), Status: "active", StartDate: time.Now().Format("2006-01-02"), LeaveAllowance: 20, PasswordHash: passwordHash, Avatar: ""}
}
func (a *API) signup(w http.ResponseWriter, r *http.Request) error {
	if a.limited("signup:"+r.RemoteAddr, 20) {
		return fail(429, "too many attempts")
	}
	var body struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if len(body.Password) < 12 || len(body.Password) > 72 || strings.TrimSpace(body.Name) == "" || len(body.Name) > 200 {
		return fail(400, "name and a 12–72 byte password are required")
	}
	op, err := a.operation(r.Context(), tokenHash(body.Token), "invitation")
	if err != nil || !validOperation(op) {
		return fail(400, "invitation is invalid or expired")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user := invitedEmployee(op, body.Name, string(hash))
	ctx := domain.WithTenant(r.Context(), op.CompanyID)
	err = a.store.Update(ctx, func(s *domain.State) error {
		for i := range s.Operations {
			v := &s.Operations[i]
			if v.ID == op.ID && validOperation(*v) {
				for _, e := range s.Employees {
					if e.Email == user.Email {
						return fail(409, "email already exists")
					}
				}
				if err := domain.ValidateManager(s, user.ID, user.ManagerID); err != nil {
					return fail(400, err.Error())
				}
				v.Status = "accepted"
				s.Employees = append(s.Employees, user)
				audit(s, user, "accepted invitation", user.ID)
				return nil
			}
		}
		return fail(400, "invitation is invalid or expired")
	})
	if err != nil {
		return err
	}
	respond(w, 201, map[string]any{"created": true, "companyId": op.CompanyID})
	return nil
}
func (a *API) mfaStatus(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	s, err := a.store.Read(r.Context())
	if err != nil {
		return err
	}
	account := s.Account(u.ID)
	c := s.Company(u.CompanyID)
	respond(w, 200, map[string]any{"enabled": account.Enabled, "required": c.RequireMFA, "recoveryCodesRemaining": len(account.RecoveryHashes), "available": len(a.cfg.MasterKey) == 32})
	return nil
}
func (a *API) mfaEnroll(w http.ResponseWriter, r *http.Request) error {
	if len(a.cfg.MasterKey) != 32 {
		return fail(503, "MFA encryption is not configured")
	}
	u := current(r)
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(body.Password)) != nil {
		return fail(403, "current password is incorrect")
	}
	secret := newTOTP()
	encrypted, err := a.seal(secret)
	if err != nil {
		return err
	}
	err = a.store.Update(r.Context(), func(s *domain.State) error {
		account := s.Account(u.ID)
		if account.Enabled {
			return fail(409, "MFA is already enabled")
		}
		account.PendingSecret = encrypted
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 200, map[string]string{"secret": secret, "uri": totpURI(u.Email, secret)})
	return nil
}
func totpURI(email, secret string) string {
	return "otpauth://totp/" + url.PathEscape("Northstar:"+email) + "?secret=" + secret + "&issuer=Northstar&algorithm=SHA1&digits=6&period=30"
}
func recoveryCodes() ([]string, []string) {
	codes := []string{}
	hashes := []string{}
	for range 10 {
		code := id()[:16]
		codes = append(codes, code)
		hashes = append(hashes, tokenHash(code))
	}
	return codes, hashes
}
func (a *API) mfaConfirm(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	var body struct {
		Code string `json:"code"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if a.limited("enroll:"+u.ID, 10) {
		return fail(429, "too many MFA attempts")
	}
	codes, hashes := recoveryCodes()
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		account := s.Account(u.ID)
		if account.Enabled || account.PendingSecret == "" {
			return fail(400, "start MFA enrollment first")
		}
		secret, err := a.open(account.PendingSecret)
		if err != nil {
			return err
		}
		step, ok := verifyTOTP(secret, body.Code, -1, time.Now())
		if !ok {
			return fail(400, "invalid authenticator code")
		}
		account.TOTPSecret = account.PendingSecret
		account.PendingSecret = ""
		account.Enabled = true
		account.LastStep = step
		account.RecoveryHashes = hashes
		revokeSessions(s, u.ID)
		audit(s, u, "enabled MFA", u.ID)
		return nil
	})
	if err != nil {
		return err
	}
	a.cookie(w, "", -1)
	respond(w, 200, map[string]any{"recoveryCodes": codes, "signInAgain": true})
	return nil
}
func (a *API) challenge(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
		Enroll    bool   `json:"enroll"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if body.Challenge == "" {
		if cookie, err := r.Cookie("northstar_mfa"); err == nil {
			body.Challenge = cookie.Value
		}
	}
	op, err := a.operation(r.Context(), tokenHash(body.Challenge), "mfa-challenge")
	if err != nil || !validOperation(op) {
		return fail(401, "challenge is invalid or expired")
	}
	ctx := domain.WithTenant(r.Context(), op.CompanyID)
	var user domain.Employee
	raw := id() + id()
	var secret string
	var codes []string
	var validation error
	err = a.store.Update(ctx, func(s *domain.State) error {
		var pending *domain.Operation
		for i := range s.Operations {
			if s.Operations[i].ID == op.ID {
				pending = &s.Operations[i]
			}
		}
		if pending == nil || !validOperation(*pending) || pending.Attempts >= 5 {
			return fail(401, "challenge is invalid or expired")
		}
		employee := s.Employee(pending.EmployeeID)
		if employee == nil || !domain.Active(*employee) {
			return fail(401, "account is inactive")
		}
		user = *employee
		account := s.Account(user.ID)
		if body.Enroll && !account.Enabled {
			secret = newTOTP()
			encrypted, err := a.seal(secret)
			if err != nil {
				return err
			}
			account.PendingSecret = encrypted
			return nil
		}
		pending.Attempts++
		encrypted := account.TOTPSecret
		if !account.Enabled {
			encrypted = account.PendingSecret
		}
		if encrypted == "" {
			validation = fail(400, "start MFA enrollment first")
			return nil
		}
		plain, err := a.open(encrypted)
		if err != nil {
			return err
		}
		step, ok := verifyTOTP(plain, body.Code, account.LastStep, time.Now())
		if !ok && account.Enabled {
			hash := tokenHash(strings.TrimSpace(body.Code))
			for i, v := range account.RecoveryHashes {
				if v == hash {
					account.RecoveryHashes = append(account.RecoveryHashes[:i], account.RecoveryHashes[i+1:]...)
					ok = true
					step = account.LastStep
					break
				}
			}
		}
		if !ok {
			validation = fail(401, "invalid authenticator or recovery code")
			return nil
		}
		account.LastStep = step
		if !account.Enabled {
			account.Enabled = true
			account.TOTPSecret = account.PendingSecret
			account.PendingSecret = ""
			var hashes []string
			codes, hashes = recoveryCodes()
			account.RecoveryHashes = hashes
		}
		pending.Status = "used"
		return a.startSession(s, user, raw, "mfa")
	})
	if err != nil {
		return err
	}
	if validation != nil {
		return validation
	}
	if secret != "" {
		respond(w, 200, map[string]string{"secret": secret, "uri": totpURI(user.Email, secret)})
		return nil
	}
	a.cookie(w, raw, 12*3600)
	http.SetCookie(w, &http.Cookie{Name: "northstar_mfa", Value: "", Path: "/api", HttpOnly: true, Secure: a.cfg.SecureCookie, MaxAge: -1})
	respond(w, 200, map[string]any{"me": user, "recoveryCodes": codes})
	return nil
}
func revokeSessions(s *domain.State, employee string) {
	sessions := []domain.Session{}
	for _, v := range s.Sessions {
		if v.EmployeeID != employee {
			sessions = append(sessions, v)
		}
	}
	s.Sessions = sessions
}
func (a *API) mfaDisable(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	var body struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(body.Password)) != nil {
		return fail(403, "current password is incorrect")
	}
	if a.limited("disable:"+u.ID, 5) {
		return fail(429, "too many MFA attempts")
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if s.Company(u.CompanyID).RequireMFA {
			return fail(403, "MFA is required by company policy")
		}
		account := s.Account(u.ID)
		secret, err := a.open(account.TOTPSecret)
		if err != nil {
			return fail(400, "MFA is not enabled")
		}
		if _, ok := verifyTOTP(secret, body.Code, account.LastStep, time.Now()); !ok {
			return fail(400, "invalid authenticator code")
		}
		account.Enabled = false
		account.TOTPSecret = ""
		account.PendingSecret = ""
		account.RecoveryHashes = nil
		account.LastStep = -1
		revokeSessions(s, u.ID)
		audit(s, u, "disabled MFA", u.ID)
		return nil
	})
	if err != nil {
		return err
	}
	a.cookie(w, "", -1)
	respond(w, 204, nil)
	return nil
}
func (a *API) ssoStart(w http.ResponseWriter, r *http.Request) error {
	if a.cfg.OIDC == nil {
		return fail(503, "Google SSO is not configured")
	}
	company, err := a.company(r.Context(), r.URL.Query().Get("company"))
	if err != nil || company.Domain == "" {
		return fail(400, "enter a company with a configured Google Workspace domain")
	}
	state, nonce, verifier := id()+id(), id()+id(), oauth2.GenerateVerifier()
	binding := id() + id()
	op := domain.Operation{ID: tokenHash(state), CompanyID: company.ID, Kind: "oauth", Status: "pending", CreatedAt: stamp(), ExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano), Data: map[string]any{"nonce": nonce, "verifier": verifier, "binding": tokenHash(binding)}}
	if err = a.store.Update(domain.WithTenant(r.Context(), company.ID), func(s *domain.State) error { s.Operations = append(s.Operations, op); return nil }); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "northstar_oauth", Value: binding, Path: "/api/auth/sso", HttpOnly: true, Secure: a.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	target := a.cfg.OAuth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("hd", company.Domain))
	http.Redirect(w, r, target, http.StatusSeeOther)
	return nil
}
func (a *API) ssoCallback(w http.ResponseWriter, r *http.Request) error {
	if a.cfg.OIDC == nil {
		return fail(503, "Google SSO is not configured")
	}
	op, err := a.operation(r.Context(), tokenHash(r.URL.Query().Get("state")), "oauth")
	cookie, cookieErr := r.Cookie("northstar_oauth")
	if err != nil || !validOperation(op) || cookieErr != nil || tokenHash(cookie.Value) != opText(op, "binding") {
		return fail(400, "SSO state is invalid or expired")
	}
	ctx := domain.WithTenant(r.Context(), op.CompanyID)
	// Consume before exchanging the code, preventing concurrent callbacks and replay.
	err = a.store.Update(ctx, func(s *domain.State) error {
		for i := range s.Operations {
			if s.Operations[i].ID == op.ID && validOperation(s.Operations[i]) {
				s.Operations[i].Status = "used"
				return nil
			}
		}
		return fail(400, "SSO state already used")
	})
	if err != nil {
		return err
	}
	exchangeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tokens, err := a.cfg.OAuth.Exchange(exchangeCtx, r.URL.Query().Get("code"), oauth2.VerifierOption(opText(op, "verifier")))
	if err != nil {
		return fail(401, "Google sign-in failed")
	}
	raw, _ := tokens.Extra("id_token").(string)
	verified, err := a.cfg.OIDC.Verifier(&oidc.Config{ClientID: a.cfg.OAuth.ClientID}).Verify(exchangeCtx, raw)
	if err != nil || verified.Nonce != opText(op, "nonce") {
		return fail(401, "Google identity verification failed")
	}
	var claims struct {
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
		Domain   string `json:"hd"`
		Name     string `json:"name"`
	}
	if err = verified.Claims(&claims); err != nil || !claims.Verified || verified.Subject == "" {
		return fail(401, "Google account email is not verified")
	}
	var user domain.Employee
	err = a.store.Update(ctx, func(s *domain.State) error {
		c := s.Company(op.CompanyID)
		if c == nil || !strings.EqualFold(claims.Domain, c.Domain) {
			return fail(403, "Google Workspace domain does not match this company")
		}
		email := strings.ToLower(claims.Email)
		for _, account := range s.Security {
			if account.GoogleSub == verified.Subject {
				u := s.Employee(account.EmployeeID)
				if u != nil && domain.Active(*u) {
					user = *u
					return nil
				}
				return fail(403, "account is inactive")
			}
		}
		for _, u := range s.Employees {
			if u.Email == email {
				if !domain.Active(u) {
					return fail(403, "account is inactive")
				}
				account := s.Account(u.ID)
				if account.GoogleSub != "" && account.GoogleSub != verified.Subject {
					return fail(403, "Google identity does not match linked account")
				}
				account.GoogleSub = verified.Subject
				user = u
				return nil
			}
		}
		for i := range s.Operations {
			invite := &s.Operations[i]
			if invite.Kind == "invitation" && validOperation(*invite) && opText(*invite, "email") == email {
				hash, err := bcrypt.GenerateFromPassword([]byte(id()+id()), bcrypt.DefaultCost)
				if err != nil {
					return err
				}
				user = invitedEmployee(*invite, claims.Name, string(hash))
				invite.Status = "accepted"
				s.Employees = append(s.Employees, user)
				s.Account(user.ID).GoogleSub = verified.Subject
				audit(s, user, "accepted SSO invitation", user.ID)
				return nil
			}
		}
		return fail(403, "an invitation is required to join this company")
	})
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "northstar_oauth", Value: "", Path: "/api/auth/sso", HttpOnly: true, Secure: a.cfg.SecureCookie, MaxAge: -1})
	a.finishLogin(w, r, user, "sso", true)
	return nil
}
func (a *API) resetRequest(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email   string `json:"email"`
		Company string `json:"company"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if a.limited("reset:"+strings.ToLower(body.Email), 3) {
		respond(w, 204, nil)
		return nil
	}
	if a.cfg.Mailer == nil {
		return fail(503, "email delivery is not configured")
	}
	users, err := a.store.(store.IdentityLookup).LoginUsers(r.Context(), strings.ToLower(strings.TrimSpace(body.Email)), body.Company)
	if err != nil {
		return err
	}
	if len(users) != 1 || !domain.Active(users[0]) {
		respond(w, 204, nil)
		return nil
	}
	u := users[0]
	raw := id() + id()
	err = a.store.Update(domain.WithTenant(r.Context(), u.CompanyID), func(s *domain.State) error {
		s.Operations = append(s.Operations, domain.Operation{ID: tokenHash(raw), CompanyID: u.CompanyID, EmployeeID: u.ID, Kind: "password-reset", Status: "pending", CreatedAt: stamp(), ExpiresAt: time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339Nano), Data: map[string]any{}})
		return a.queueMail(s, u.Email, "Reset your Northstar password", "Reset your password: "+a.cfg.AppURL+"/reset-password?token="+url.QueryEscape(raw)+"\n\nThis link expires in 30 minutes. If you did not request it, ignore this message.")
	})
	if err != nil {
		return err
	}
	respond(w, 204, nil)
	return nil
}
func (a *API) resetPassword(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if len(body.Password) < 12 || len(body.Password) > 72 {
		return fail(400, "password must contain 12–72 bytes")
	}
	op, err := a.operation(r.Context(), tokenHash(body.Token), "password-reset")
	if err != nil || !validOperation(op) {
		return fail(400, "reset link is invalid or expired")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	err = a.store.Update(domain.WithTenant(r.Context(), op.CompanyID), func(s *domain.State) error {
		for i := range s.Operations {
			v := &s.Operations[i]
			if v.ID == op.ID && validOperation(*v) {
				u := s.Employee(v.EmployeeID)
				if u == nil || !domain.Active(*u) {
					return fail(400, "reset link is invalid or expired")
				}
				u.PasswordHash = string(hash)
				v.Status = "used"
				revokeSessions(s, u.ID)
				audit(s, *u, "reset password", u.ID)
				return nil
			}
		}
		return fail(400, "reset link is invalid or expired")
	})
	if err != nil {
		return err
	}
	respond(w, 204, nil)
	return nil
}
