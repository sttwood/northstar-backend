package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/seed"
	"northstar/backend/internal/store"
	"testing"
)

func harness(t *testing.T) http.Handler {
	t.Helper()
	s, err := seed.Demo()
	if err != nil {
		t.Fatal(err)
	}
	return New(store.NewMemory(s), Config{Origins: []string{"http://localhost:3000"}, UploadDir: t.TempDir()})
}
func call(t *testing.T, h http.Handler, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, reader)
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func signIn(t *testing.T, h http.Handler, email string) *http.Cookie {
	t.Helper()
	w := call(t, h, "POST", "/api/auth/login", map[string]string{"email": email, "password": seed.DemoPassword}, nil)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 || !cookies[0].HttpOnly {
		t.Fatal("secure httpOnly session missing")
	}
	return cookies[0]
}
func assertCode(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("expected %d got %d: %s", code, w.Code, w.Body)
	}
}
func TestAuthenticationPrivacyAndApproval(t *testing.T) {
	h := harness(t)
	assertCode(t, call(t, h, "GET", "/api/bootstrap", nil, nil), 401)
	maya := signIn(t, h, "maya@northstar.test")
	w := call(t, h, "GET", "/api/bootstrap", nil, maya)
	assertCode(t, w, 200)
	var body struct {
		Employees []domain.Employee `json:"employees"`
		Records   []domain.Record   `json:"records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, e := range body.Employees {
		if e.ID != "maya" && (e.Salary != 0 || e.Phone != "") {
			t.Fatal("private employee details leaked")
		}
	}
	for _, r := range body.Records {
		if r.Kind == "leave" && r.EmployeeID != "maya" {
			if _, exists := r.Data["reason"]; exists {
				t.Fatal("leave reason leaked")
			}
		}
	}
	if bytes.Contains(w.Body.Bytes(), []byte("password")) {
		t.Fatal("password hash leaked")
	}
	assertCode(t, call(t, h, "PATCH", "/api/records/ot3", map[string]any{"status": "approved"}, maya), 400)
	assertCode(t, call(t, h, "PATCH", "/api/employees/maya", map[string]any{"salary": 999999}, maya), 403)
	assertCode(t, call(t, h, "PATCH", "/api/employees/jordan", map[string]any{"name": "Spoofed"}, maya), 403)
	owner := signIn(t, h, "emma@northstar.test")
	assertCode(t, call(t, h, "PATCH", "/api/records/ot3", map[string]any{"status": "approved"}, owner), 200)
	assertCode(t, call(t, h, "PATCH", "/api/records/ot3", map[string]any{"status": "rejected", "data": map[string]any{"decisionNote": "No"}}, owner), 400)
	assertCode(t, call(t, h, "POST", "/api/auth/logout", nil, maya), 204)
	assertCode(t, call(t, h, "GET", "/api/bootstrap", nil, maya), 401)
}
func TestLeaveCannotForgeDaysOrOverlap(t *testing.T) {
	h := harness(t)
	maya := signIn(t, h, "maya@northstar.test")
	payload := map[string]any{"kind": "leave", "employeeId": "maya", "title": "Vacation", "status": "pending", "data": map[string]any{"type": "vacation", "startDate": "2026-10-22", "endDate": "2026-10-26", "days": 999}}
	w := call(t, h, "POST", "/api/records", payload, maya)
	assertCode(t, w, 201)
	var r domain.Record
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	if domain.Number(r, "days") != 2 {
		t.Fatal("client supplied days trusted")
	}
	assertCode(t, call(t, h, "POST", "/api/records", payload, maya), 400)
	payload["status"] = "approved"
	assertCode(t, call(t, h, "POST", "/api/records", payload, maya), 400)
}
func uploadPDF(t *testing.T, h http.Handler, cookie *http.Cookie, employee string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "private.pdf")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("%PDF-1.4\nprivate payroll\n%%EOF"))
	for k, v := range map[string]string{"employeeId": employee, "category": "payslip", "month": "10", "year": "2026"} {
		_ = writer.WriteField(k, v)
	}
	_ = writer.Close()
	r := httptest.NewRequest("POST", "/api/files", &buf)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestPayslipsArePrivateAndManagerUploaded(t *testing.T) {
	h := harness(t)
	owner := signIn(t, h, "emma@northstar.test")
	maya := signIn(t, h, "maya@northstar.test")
	jordan := signIn(t, h, "jordan@northstar.test")
	assertCode(t, uploadPDF(t, h, maya, "maya"), 403)
	w := uploadPDF(t, h, owner, "maya")
	assertCode(t, w, 201)
	var file domain.File
	_ = json.Unmarshal(w.Body.Bytes(), &file)
	assertCode(t, call(t, h, "GET", "/api/files/"+file.ID, nil, jordan), 403)
	assertCode(t, call(t, h, "GET", "/api/files/"+file.ID, nil, maya), 200)
	assertCode(t, call(t, h, "DELETE", "/api/files/"+file.ID, nil, maya), 403)
	assertCode(t, call(t, h, "DELETE", "/api/files/"+file.ID, nil, owner), 204)
	assertCode(t, call(t, h, "GET", "/api/files/"+file.ID, nil, maya), 404)
}
func TestCORSAndUnknownFields(t *testing.T) {
	h := harness(t)
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.Header.Set("Origin", "https://untrusted.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertCode(t, w, 403)
	assertCode(t, call(t, h, "POST", "/api/auth/login", map[string]any{"email": "maya@northstar.test", "password": seed.DemoPassword, "role": "owner"}, nil), 400)
}
