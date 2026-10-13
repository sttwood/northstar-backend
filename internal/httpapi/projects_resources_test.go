package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/seed"
	"northstar/backend/internal/store"
	"testing"
)

func uploadResource(t *testing.T, h http.Handler, cookie *http.Cookie, name string, b []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(b)
	for key, value := range fields {
		_ = writer.WriteField(key, value)
	}
	_ = writer.Close()
	r := httptest.NewRequest("POST", "/api/files", &buf)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestProjectResourcesAndOwnerTemplateWorkflow(t *testing.T) {
	state, err := seed.Demo()
	if err != nil {
		t.Fatal(err)
	}
	hr := *state.Employee("emma")
	hr.ID = "hr"
	hr.Role = "hr"
	hr.Email = "hr@example.test"
	state.Employees = append(state.Employees, hr)
	h := New(store.NewMemory(state), Config{Origins: []string{"http://localhost:3000"}, UploadDir: t.TempDir()})
	owner := signIn(t, h, "emma@northstar.test")
	employee := signIn(t, h, "maya@northstar.test")
	outsider := signIn(t, h, "jordan@northstar.test")
	hrCookie := signIn(t, h, hr.Email)
	createProject := func(title, parent string) domain.Record {
		payload := map[string]any{"kind": "project", "employeeId": "emma", "title": title, "status": "active", "data": map[string]any{"parentId": parent, "members": []string{"maya"}, "startDate": "2026-10-01", "dueDate": "2026-12-31"}}
		w := call(t, h, "POST", "/api/records", payload, owner)
		assertCode(t, w, 201)
		var r domain.Record
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		return r
	}
	parent := createProject("sawasdee", "")
	child := createProject("sawasdee-api", parent.ID)
	cycle := map[string]any{}
	for k, v := range parent.Data {
		cycle[k] = v
	}
	cycle["parentId"] = child.ID
	assertCode(t, call(t, h, "PATCH", "/api/records/"+parent.ID, map[string]any{"data": cycle}, owner), 400)
	assertCode(t, call(t, h, "DELETE", "/api/records/"+parent.ID, nil, owner), 400)
	fields := map[string]string{"category": "project-resource", "employeeId": "jordan", "projectId": child.ID, "resourceKind": "api-spec", "description": "Synthetic API schema"}
	contents := []byte(`{"openapi":"3.1.0","info":{"title":"QA API","version":"1"}}`)
	w := uploadResource(t, h, employee, "openapi.json", contents, fields)
	assertCode(t, w, 201)
	var file domain.File
	_ = json.Unmarshal(w.Body.Bytes(), &file)
	if file.EmployeeID != "maya" || file.MimeType != "application/json" {
		t.Fatal("unsafe resource metadata")
	}
	downloaded := call(t, h, "GET", "/api/files/"+file.ID, nil, employee)
	assertCode(t, downloaded, 200)
	if !bytes.Equal(downloaded.Body.Bytes(), contents) {
		t.Fatal("resource changed")
	}
	assertCode(t, call(t, h, "GET", "/api/files/"+file.ID, nil, outsider), 403)
	listed := call(t, h, "GET", "/api/v2/files?kind=project-resource&projectId="+child.ID+"&limit=1", nil, employee)
	assertCode(t, listed, 200)
	if !bytes.Contains(listed.Body.Bytes(), []byte(file.ID)) {
		t.Fatal("member file missing")
	}
	listed = call(t, h, "GET", "/api/v2/files?kind=project-resource&projectId="+child.ID, nil, outsider)
	assertCode(t, listed, 200)
	if bytes.Contains(listed.Body.Bytes(), []byte(file.ID)) {
		t.Fatal("resource listing leaked")
	}
	assertCode(t, call(t, h, "DELETE", "/api/records/"+child.ID, nil, owner), 400)
	assertCode(t, uploadResource(t, h, outsider, "openapi.json", contents, fields), 403)
	assertCode(t, uploadResource(t, h, employee, "openapi.json", []byte("invalid JSON"), fields), 400)
	payload := map[string]any{"kind": "template", "employeeId": "emma", "title": "Synthetic table template", "status": "active", "data": map[string]any{"layout": "table", "fields": []string{"Base salary", "Net pay"}, "earningFields": []string{"Base salary"}}}
	assertCode(t, call(t, h, "POST", "/api/records", payload, hrCookie), 400)
	w = call(t, h, "POST", "/api/records", payload, owner)
	assertCode(t, w, 201)
	var template domain.Record
	_ = json.Unmarshal(w.Body.Bytes(), &template)
	assertCode(t, call(t, h, "PATCH", "/api/records/"+template.ID, map[string]any{"title": "Hijacked"}, hrCookie), 403)
	refFields := map[string]string{"category": "payslip-reference", "recordId": template.ID, "employeeId": "maya"}
	w = uploadResource(t, h, owner, "synthetic-reference.pdf", []byte("%PDF-1.4\nSynthetic only\n%%EOF"), refFields)
	assertCode(t, w, 201)
	var ref domain.File
	_ = json.Unmarshal(w.Body.Bytes(), &ref)
	for _, c := range []*http.Cookie{employee, hrCookie} {
		assertCode(t, call(t, h, "GET", "/api/files/"+ref.ID, nil, c), 403)
		assertCode(t, uploadResource(t, h, c, "reference.pdf", []byte("%PDF-1.4"), refFields), 403)
		for _, path := range []string{"/api/bootstrap", "/api/v2/files?kind=payslip-reference&recordId=" + template.ID} {
			w = call(t, h, "GET", path, nil, c)
			assertCode(t, w, 200)
			if bytes.Contains(w.Body.Bytes(), []byte(ref.Name)) {
				t.Fatal("reference metadata leaked")
			}
		}
	}
	assertCode(t, call(t, h, "DELETE", "/api/records/"+template.ID, nil, owner), 400)
	assertCode(t, call(t, h, "DELETE", "/api/files/"+ref.ID, nil, owner), 204)
	assertCode(t, call(t, h, "DELETE", "/api/records/"+template.ID, nil, owner), 204)
	assertCode(t, call(t, h, "DELETE", "/api/files/"+file.ID, nil, employee), 204)
	assertCode(t, call(t, h, "DELETE", "/api/records/"+child.ID, nil, owner), 204)
	assertCode(t, call(t, h, "DELETE", "/api/records/"+parent.ID, nil, owner), 204)
}

func TestOfficeResourcesRejectMacrosAndInvalidTypes(t *testing.T) {
	office := func(macro bool) []byte {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		for _, name := range []string{"[Content_Types].xml", "xl/workbook.xml"} {
			f, _ := w.Create(name)
			_, _ = f.Write([]byte("<document/>"))
		}
		if macro {
			f, _ := w.Create("xl/vbaProject.bin")
			_, _ = f.Write([]byte("macro"))
		}
		_ = w.Close()
		return b.Bytes()
	}
	if mime, err := uploadType("project-resource", "dictionary.xlsx", office(false)); err != nil || mime != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("xlsx rejected: %v", err)
	}
	if _, err := uploadType("project-resource", "dictionary.xlsx", office(true)); err == nil {
		t.Fatal("macros accepted")
	}
	for name, contents := range map[string][]byte{"bad.xlsx": []byte("not a zip"), "danger.exe": []byte("MZ"), "image.svg": []byte("<svg><script/></svg>"), "bad.json": []byte("<html/>")} {
		if _, err := uploadType("project-resource", name, contents); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}
