package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"northstar/backend/internal/domain"
	"testing"
)

func TestOwnerOnlyAdministrativeActionsAndEnvironmentFiles(t *testing.T) {
	h := harness(t)
	owner := signIn(t, h, "emma@northstar.test")
	hr := signIn(t, h, "mia@northstar.test")
	employee := signIn(t, h, "maya@northstar.test")
	for _, cookie := range []*http.Cookie{hr, employee} {
		for _, kind := range []string{"holiday", "benefit", "onboarding", "template"} {
			w := call(t, h, "POST", "/api/records", map[string]any{"kind": kind, "employeeId": "maya", "title": "Forbidden", "status": "active", "data": map[string]any{}}, cookie)
			assertCode(t, w, 400)
		}
		for _, path := range []string{"/api/payroll/calculate", "/api/payroll/finalize", "/api/payroll/opening", "/api/invitations"} {
			assertCode(t, call(t, h, "POST", path, map[string]any{}, cookie), 403)
		}
		assertCode(t, uploadPDF(t, h, cookie, "maya"), 403)
		expected := 403
		if cookie == employee {
			expected = 400
		}
		assertCode(t, call(t, h, "PATCH", "/api/records/ot3", map[string]any{"status": "approved"}, cookie), expected)
		inbox := call(t, h, "GET", "/api/v2/records?approvals=true", nil, cookie)
		assertCode(t, inbox, 200)
		var page struct {
			Items []domain.Record `json:"items"`
		}
		_ = json.Unmarshal(inbox.Body.Bytes(), &page)
		if len(page.Items) != 0 {
			t.Fatal("non-owner approval inbox exposed")
		}
	}
	fields := map[string]string{"category": "project-resource", "projectId": "atlas", "resourceKind": "document", "environment": "sit"}
	contents := []byte("Synthetic SIT guide")
	uploaded := uploadResource(t, h, owner, "guide.txt", contents, fields)
	assertCode(t, uploaded, 201)
	var file domain.File
	_ = json.Unmarshal(uploaded.Body.Bytes(), &file)
	if file.Environment != "sit" {
		t.Fatal("environment metadata lost")
	}
	assertCode(t, uploadResource(t, h, employee, "guide.txt", contents, fields), 403)
	assertCode(t, call(t, h, "GET", "/api/files/"+file.ID, nil, employee), 200)
	for _, env := range []string{"dev", "sit", "uat", "prod"} {
		page := call(t, h, "GET", "/api/v2/files?kind=project-resource&projectId=atlas&environment="+env, nil, employee)
		assertCode(t, page, 200)
		found := bytes.Contains(page.Body.Bytes(), []byte(file.ID))
		if found != (env == "sit") {
			t.Fatal("environment query mixed document files")
		}
	}
	fields["environment"] = "secret"
	assertCode(t, uploadResource(t, h, owner, "guide.txt", contents, fields), 400)
	assertCode(t, call(t, h, "DELETE", "/api/files/"+file.ID, nil, employee), 403)
	assertCode(t, call(t, h, "DELETE", "/api/files/"+file.ID, nil, owner), 204)
}
