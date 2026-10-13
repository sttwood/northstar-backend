package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/seed"
	"northstar/backend/internal/store"
	"strings"
	"testing"
	"time"
)

type captureMail struct {
	Messages []string
	Fail     bool
}

func (m *captureMail) Send(ctx context.Context, to, subject, body string) error {
	if m.Fail {
		return errors.New("provider unavailable")
	}
	m.Messages = append(m.Messages, to+"\n"+subject+"\n"+body)
	return nil
}
func productionHarness(t *testing.T) (*API, http.Handler, *store.Memory, *captureMail) {
	t.Helper()
	s, err := seed.Demo()
	if err != nil {
		t.Fatal(err)
	}
	repo := store.NewMemory(s)
	mail := &captureMail{}
	a, h := NewService(repo, Config{UploadDir: t.TempDir(), Origins: []string{"http://localhost:3000"}, AppURL: "http://localhost:3000", MasterKey: []byte(strings.Repeat("k", 32)), Mailer: mail})
	return a, h, repo, mail
}
func TestInvitationsTenancyAndOutboxDelivery(t *testing.T) {
	a, h, repo, mail := productionHarness(t)
	owner := signIn(t, h, "emma@northstar.test")
	w := call(t, h, "POST", "/api/invitations", map[string]any{"name": "New colleague", "email": "new@northstar.test", "role": "employee", "managerId": "maya"}, owner)
	assertCode(t, w, 201)
	var invite struct {
		InviteURL string `json:"inviteUrl"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &invite)
	raw := strings.Split(strings.Split(invite.InviteURL, "token=")[1], "&")[0]
	body := map[string]any{"name": "New colleague", "password": "NewEmployee2026!", "token": raw}
	assertCode(t, call(t, h, "POST", "/api/auth/signup", body, nil), 201)
	assertCode(t, call(t, h, "POST", "/api/auth/signup", body, nil), 400)
	newLogin := call(t, h, "POST", "/api/auth/login", map[string]string{"email": "new@northstar.test", "password": "NewEmployee2026!"}, nil)
	assertCode(t, newLogin, 200)
	newUser := newLogin.Result().Cookies()[0]
	if err := a.ProcessJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mail.Messages) != 1 || !strings.Contains(mail.Messages[0], invite.InviteURL) {
		t.Fatal("invitation email was not delivered by worker")
	}
	w = call(t, h, "POST", "/api/companies", map[string]any{"name": "Second company", "slug": "second-company"}, owner)
	assertCode(t, w, 201)
	var created struct{ Company domain.Company }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	w = call(t, h, "POST", "/api/auth/login", map[string]string{"company": "second-company", "email": "emma@northstar.test", "password": seed.DemoPassword}, nil)
	assertCode(t, w, 200)
	second := w.Result().Cookies()[0]
	assertCode(t, call(t, h, "PATCH", "/api/employees/maya", map[string]any{"salary": 100}, second), 404)
	assertCode(t, call(t, h, "GET", "/api/files/sample-payslip-maya-2026-09", nil, second), 404)
	w = call(t, h, "GET", "/api/v2/bootstrap", nil, second)
	assertCode(t, w, 200)
	if strings.Contains(w.Body.String(), "maya@") || strings.Contains(w.Body.String(), "Atlas") {
		t.Fatal("cross-company bootstrap leak")
	}
	assertCode(t, call(t, h, "POST", "/api/companies", map[string]any{"name": "Forbidden", "slug": "forbidden"}, newUser), 403)
	state, err := repo.Read(domain.WithTenant(context.Background(), created.Company.ID))
	if err != nil || len(state.Employees) != 1 || len(state.Records) != 0 {
		t.Fatalf("tenant state not isolated: %v", err)
	}
}
func TestMFAEnrollmentChallengeReplayAndRecovery(t *testing.T) {
	_, h, _, _ := productionHarness(t)
	maya := signIn(t, h, "maya@northstar.test")
	w := call(t, h, "POST", "/api/security/mfa/enroll", map[string]any{"password": seed.DemoPassword}, maya)
	assertCode(t, w, 200)
	var enrollment struct{ Secret string }
	_ = json.Unmarshal(w.Body.Bytes(), &enrollment)
	code := totpAt(enrollment.Secret, time.Now().Unix()/30)
	w = call(t, h, "POST", "/api/security/mfa/confirm", map[string]any{"code": code}, maya)
	assertCode(t, w, 200)
	var recovery struct{ RecoveryCodes []string }
	_ = json.Unmarshal(w.Body.Bytes(), &recovery)
	if len(recovery.RecoveryCodes) != 10 {
		t.Fatal("recovery codes missing")
	}
	assertCode(t, call(t, h, "GET", "/api/bootstrap", nil, maya), 401)
	login := func() string {
		t.Helper()
		w := call(t, h, "POST", "/api/auth/login", map[string]string{"email": "maya@northstar.test", "password": seed.DemoPassword}, nil)
		assertCode(t, w, 200)
		if len(w.Result().Cookies()) > 0 {
			t.Fatal("password bypassed MFA")
		}
		var challenge struct{ Challenge string }
		_ = json.Unmarshal(w.Body.Bytes(), &challenge)
		return challenge.Challenge
	}
	challenge := login()
	assertCode(t, call(t, h, "POST", "/api/auth/mfa/challenge", map[string]any{"challenge": challenge, "code": code}, nil), 401)
	w = call(t, h, "POST", "/api/auth/mfa/challenge", map[string]any{"challenge": challenge, "code": recovery.RecoveryCodes[0]}, nil)
	assertCode(t, w, 200)
	assertCode(t, call(t, h, "GET", "/api/bootstrap", nil, w.Result().Cookies()[0]), 200)
	assertCode(t, call(t, h, "POST", "/api/auth/mfa/challenge", map[string]any{"challenge": challenge, "code": recovery.RecoveryCodes[1]}, nil), 401)
	challenge = login()
	assertCode(t, call(t, h, "POST", "/api/auth/mfa/challenge", map[string]any{"challenge": challenge, "code": recovery.RecoveryCodes[0]}, nil), 401)
	for range 4 {
		assertCode(t, call(t, h, "POST", "/api/auth/mfa/challenge", map[string]any{"challenge": challenge, "code": "000000000"}, nil), 401)
	}
	assertCode(t, call(t, h, "POST", "/api/auth/mfa/challenge", map[string]any{"challenge": challenge, "code": recovery.RecoveryCodes[1]}, nil), 401)
}
func TestOwnerApprovalsOffboardingAndRetention(t *testing.T) {
	a, h, repo, _ := productionHarness(t)
	owner := signIn(t, h, "emma@northstar.test")
	maya := signIn(t, h, "maya@northstar.test")
	jordan := signIn(t, h, "jordan@northstar.test")
	hr := signIn(t, h, "mia@northstar.test")
	ctx := domain.WithTenant(context.Background(), "default")
	err := repo.Update(ctx, func(s *domain.State) error {
		s.Company("default").ApprovalStages = []string{"manager", "hr"}
		s.Employee("maya").ManagerID = "jordan"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w := call(t, h, "POST", "/api/records", map[string]any{"kind": "request", "employeeId": "maya", "title": "New equipment", "status": "pending", "data": map[string]any{"category": "Equipment", "description": "Replacement keyboard"}}, maya)
	assertCode(t, w, 201)
	var record domain.Record
	_ = json.Unmarshal(w.Body.Bytes(), &record)
	assertCode(t, call(t, h, "PATCH", "/api/records/"+record.ID, map[string]any{"status": "approved"}, jordan), 403)
	assertCode(t, call(t, h, "PATCH", "/api/records/"+record.ID, map[string]any{"status": "approved"}, hr), 403)
	assertCode(t, call(t, h, "DELETE", "/api/records/"+record.ID, nil, jordan), 403)
	assertCode(t, call(t, h, "PATCH", "/api/records/"+record.ID, map[string]any{"status": "approved"}, owner), 200)
	assertCode(t, call(t, h, "POST", "/api/employees/maya/offboard", map[string]any{"transferTo": "jordan", "legalHold": true}, owner), 204)
	assertCode(t, call(t, h, "GET", "/api/bootstrap", nil, maya), 401)
	err = repo.Update(ctx, func(s *domain.State) error {
		s.Employee("maya").RetainUntil = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
		s.Records = append(s.Records, domain.Record{ID: "retention-payroll", CompanyID: "default", EmployeeID: "maya", Kind: "payroll", Status: "finalized", Data: map[string]any{"gross": 65000}})
		s.Files = append(s.Files, domain.File{ID: "retained-project-resource", CompanyID: "default", EmployeeID: "maya", Category: "project-resource", ProjectID: "atlas", Name: "shared-api.json", MimeType: "application/json"})
		s.Operations = append(s.Operations, domain.Operation{ID: "retention-mail", CompanyID: "default", Kind: "email", Status: "queued", Data: map[string]any{"to": "maya@northstar.test", "body": "encrypted-private-body"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	count, err := a.applyRetention(context.Background(), "default")
	if err != nil || count != 0 {
		t.Fatal("legal hold ignored")
	}
	assertCode(t, call(t, h, "PATCH", "/api/employees/maya/retention", map[string]any{"legalHold": false}, owner), 204)
	count, err = a.applyRetention(context.Background(), "default")
	if err != nil || count != 1 {
		t.Fatalf("retention failed %v", err)
	}
	state, _ := repo.Read(ctx)
	if state.Employee("maya").Salary != 0 || state.Employee("maya").Status != "anonymized" {
		t.Fatal("personal data retained after purge")
	}
	for _, record := range state.Records {
		if record.ID == "retention-payroll" {
			t.Fatal("salary ledger retained after purge")
		}
	}
	for _, op := range state.Operations {
		if op.ID == "retention-mail" {
			t.Fatal("recipient PII retained after purge")
		}
	}
	retainedResource := false
	for _, file := range state.Files {
		if file.ID == "retained-project-resource" {
			retainedResource = true
			continue
		}
		if file.EmployeeID == "maya" {
			t.Fatal("expired payroll metadata retained")
		}
	}
	if !retainedResource {
		t.Fatal("shared project resource deleted with uploader payroll")
	}
}
func TestFailedDeliveryCanBeRetriedByManagers(t *testing.T) {
	a, h, repo, mail := productionHarness(t)
	owner := signIn(t, h, "emma@northstar.test")
	employee := signIn(t, h, "maya@northstar.test")
	assertCode(t, call(t, h, "POST", "/api/invitations", map[string]any{"name": "Retry colleague", "email": "retry@northstar.test", "role": "employee"}, owner), 201)
	ctx := domain.WithTenant(context.Background(), "default")
	jobID := ""
	err := repo.Update(ctx, func(s *domain.State) error {
		for i := range s.Operations {
			if s.Operations[i].Kind == "email" {
				s.Operations[i].Attempts = 7
				jobID = s.Operations[i].ID
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mail.Fail = true
	if err = a.ProcessJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _ := repo.Read(ctx)
	failed := false
	for _, op := range state.Operations {
		failed = failed || op.ID == jobID && op.Status == "failed"
	}
	if !failed {
		t.Fatal("retry limit ignored")
	}
	assertCode(t, call(t, h, "POST", "/api/operations/"+jobID+"/retry", map[string]any{}, employee), 403)
	assertCode(t, call(t, h, "POST", "/api/operations/"+jobID+"/retry", map[string]any{}, owner), 204)
	mail.Fail = false
	if err = a.ProcessJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _ = repo.Read(ctx)
	for _, op := range state.Operations {
		if op.ID == jobID && (op.Status != "sent" || op.Data["body"] != nil) {
			t.Fatal("delivery or secret cleanup failed")
		}
	}
	if len(mail.Messages) != 1 {
		t.Fatal("retry was not delivered exactly once in controlled test")
	}
}
