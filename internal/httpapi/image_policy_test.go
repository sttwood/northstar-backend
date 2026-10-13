package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/seed"
	"northstar/backend/internal/store"
	"os"
	"path/filepath"
	"testing"
)

type imagePolicyObjects struct{ values map[string][]byte }
type rejectingImageScanner struct{}

func (rejectingImageScanner) Name() string { return "clamav" }

func (rejectingImageScanner) Scan(context.Context, []byte) error {
	return errors.New("scanner unavailable")
}
func (s *imagePolicyObjects) Name() string { return "s3" }
func (s *imagePolicyObjects) Put(_ context.Context, key string, b []byte) error {
	s.values[key] = append([]byte{}, b...)
	return nil
}
func (s *imagePolicyObjects) Get(_ context.Context, key string) ([]byte, error) {
	b, ok := s.values[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return b, nil
}
func (s *imagePolicyObjects) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func TestImagesOnlyRequiresCoreAndRealPrivateStorage(t *testing.T) {
	coreEnvironment(t)
	t.Setenv("UPLOAD_POLICY", "images-only")
	cfg, err := Configuration(context.Background(), []string{"https://intranet.example.test"}, t.TempDir())
	if err != nil || cfg.AllowImageUploads || !cfg.DisableUploads {
		t.Fatal("images enabled without storage")
	}
	bucket := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" || r.URL.Path != "/synthetic-images" {
			t.Errorf("unexpected S3 startup request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer bucket.Close()
	ca := filepath.Join(t.TempDir(), "test-ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: bucket.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("S3_ENDPOINT", bucket.URL)
	t.Setenv("S3_BUCKET", "synthetic-images")
	t.Setenv("AWS_REGION", "auto")
	t.Setenv("AWS_ACCESS_KEY_ID", "synthetic-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic-secret")
	t.Setenv("AWS_CA_BUNDLE", ca)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	cfg, err = Configuration(context.Background(), []string{"https://intranet.example.test"}, t.TempDir())
	if err != nil || !cfg.AllowImageUploads || !cfg.DisableUploads || cfg.Blobs.Name() != "s3" || cfg.Scanner != nil {
		t.Fatalf("image policy configuration: %+v %v", cfg, err)
	}
	t.Setenv("UPLOAD_POLICY", "")
	cfg, err = Configuration(context.Background(), []string{"https://intranet.example.test"}, t.TempDir())
	if err != nil || cfg.UploadPolicy != "scanned" || cfg.AllowImageUploads || !cfg.DisableUploads {
		t.Fatal("default policy allowed unscanned images with storage only")
	}
	t.Setenv("UPLOAD_POLICY", "images-only")
	t.Setenv("PRODUCTION_PROFILE", "full")
	if _, err = Configuration(context.Background(), []string{"https://intranet.example.test"}, t.TempDir()); err == nil {
		t.Fatal("full profile accepted normalization instead of antivirus")
	}
	t.Setenv("UPLOAD_POLICY", "typo")
	if _, err = Configuration(context.Background(), []string{"https://intranet.example.test"}, t.TempDir()); err == nil {
		t.Fatal("unknown policy accepted")
	}
}

func TestImagesOnlyNormalizesPixelsAndKeepsGeneralFilesBlocked(t *testing.T) {
	s, err := seed.Demo()
	if err != nil {
		t.Fatal(err)
	}
	objects := &imagePolicyObjects{values: map[string][]byte{}}
	h := New(store.NewMemory(s), Config{Production: true, ProductionProfile: "core", UploadPolicy: "images-only", DisableUploads: true, AllowImageUploads: true, Blobs: objects})
	owner := signIn(t, h, "emma@northstar.test")
	employee := signIn(t, h, "maya@northstar.test")
	upload := func(category, name string, b []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("category", category)
		_ = form.WriteField("employeeId", "emma")
		part, _ := form.CreateFormFile("file", name)
		_, _ = part.Write(b)
		_ = form.Close()
		req := httptest.NewRequest("POST", "/api/files", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	var raw bytes.Buffer
	_ = png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 900, 650)))
	input := append(raw.Bytes(), []byte("<script>synthetic trailing payload</script>")...)
	assertCode(t, upload("avatar", "photo.png", input, nil), 401)
	w := upload("avatar", "photo.png", input, employee)
	assertCode(t, w, 201)
	var avatar domain.File
	_ = json.Unmarshal(w.Body.Bytes(), &avatar)
	if avatar.EmployeeID != "maya" || avatar.ScanStatus != "image-normalized" || avatar.MimeType != "image/png" || len(objects.values) != 1 {
		t.Fatal("image identity/status mismatch")
	}
	normalized := objects.values[avatar.StorageKey]
	if bytes.Contains(normalized, []byte("<script>")) {
		t.Fatal("stored original bytes instead of pixels")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(normalized))
	if err != nil || config.Width > 512 || config.Height > 512 {
		t.Fatal("avatar dimensions not normalized")
	}
	assertCode(t, call(t, h, "GET", "/api/files/"+avatar.ID, nil, nil), 401)
	download := call(t, h, "GET", "/api/files/"+avatar.ID, nil, employee)
	assertCode(t, download, 200)
	if !bytes.Equal(download.Body.Bytes(), normalized) {
		t.Fatal("private storage roundtrip failed")
	}
	w = upload("community-image", "help.png", input, employee)
	assertCode(t, w, 201)
	var community domain.File
	_ = json.Unmarshal(w.Body.Bytes(), &community)
	if community.ScanStatus != "image-normalized" {
		t.Fatal("community image misreported as scanned")
	}
	for _, category := range []string{"payslip", "document", "attachment", "project-resource", "payslip-reference"} {
		assertCode(t, upload(category, "blocked.png", input, owner), 503)
	}
	for _, bad := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"), []byte("not an image"), raw.Bytes()[:35], bytes.Repeat([]byte("x"), 2*1024*1024+1)} {
		assertCode(t, upload("avatar", "fake.png", bad, employee), 400)
	}
	if len(objects.values) != 2 {
		t.Fatal("blocked files reached object storage")
	}
	w = call(t, h, "GET", "/api/company", nil, owner)
	assertCode(t, w, 200)
	var status struct {
		Integrations map[string]any `json:"integrations"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	if status.Integrations["fileUploads"] != false || status.Integrations["imageUploads"] != true || status.Integrations["scanner"] != "disabled" || status.Integrations["storage"] != "s3" {
		t.Fatal("capabilities imply antivirus or general uploads")
	}
	assertCode(t, call(t, h, "DELETE", "/api/files/"+avatar.ID, nil, employee), 204)
}

func TestImagesOnlyNeverFallsBackAfterScannerFailure(t *testing.T) {
	s, err := seed.Demo()
	if err != nil {
		t.Fatal(err)
	}
	objects := &imagePolicyObjects{values: map[string][]byte{}}
	cfg := Config{Production: true, ProductionProfile: "core", UploadPolicy: "images-only", DisableUploads: true, AllowImageUploads: true, Blobs: objects}
	var jpegBytes bytes.Buffer
	if err = jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 80, 60)), nil); err != nil {
		t.Fatal(err)
	}
	for _, scan := range []bool{false, true} {
		if scan {
			cfg.Scanner = rejectingImageScanner{}
		}
		h := New(store.NewMemory(s), cfg)
		employee := signIn(t, h, "maya@northstar.test")
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("category", "avatar")
		part, _ := form.CreateFormFile("file", "profile.jpg")
		_, _ = part.Write(jpegBytes.Bytes())
		_ = form.Close()
		req := httptest.NewRequest("POST", "/api/files", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		req.AddCookie(employee)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if scan {
			assertCode(t, w, 422)
		} else {
			assertCode(t, w, 201)
			var f domain.File
			_ = json.Unmarshal(w.Body.Bytes(), &f)
			if f.ScanStatus != "image-normalized" || f.MimeType != "image/jpeg" {
				t.Fatal("JPEG policy mismatch")
			}
		}
	}
	if len(objects.values) != 1 {
		t.Fatal("scanner failure fell back to normalization")
	}
}
