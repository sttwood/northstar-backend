package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"northstar/backend/internal/domain"
	"strings"
	"sync"
	"testing"
)

func communityRecordResponse(t *testing.T, h http.Handler, path string, body any, cookie *http.Cookie) domain.Record {
	t.Helper()
	w := call(t, h, "POST", path, body, cookie)
	assertCode(t, w, 201)
	var r domain.Record
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	return r
}
func TestCommunitySolutionsAwardsAndPermissions(t *testing.T) {
	h := harness(t)
	maya := signIn(t, h, "maya@northstar.test")
	jordan := signIn(t, h, "jordan@northstar.test")
	outsider := signIn(t, h, "alex@northstar.test")
	owner := signIn(t, h, "emma@northstar.test")
	post := communityRecordResponse(t, h, "/api/community/posts", map[string]any{"title": "Help me run Go", "type": "help", "body": "How do I run the API?", "tags": []string{"junior", "go"}}, maya)
	reply := communityRecordResponse(t, h, "/api/community/posts/"+post.ID+"/answers", map[string]any{"body": "Run go run ./cmd/server in the backend folder."}, jordan)
	self := communityRecordResponse(t, h, "/api/community/posts/"+post.ID+"/answers", map[string]any{"body": "I also tried this."}, maya)
	ownerAnswer := communityRecordResponse(t, h, "/api/community/posts/"+post.ID+"/answers", map[string]any{"body": "An owner answer cannot award itself."}, owner)
	assertCode(t, call(t, h, "POST", "/api/community/posts/"+post.ID+"/resolve", map[string]any{"answerId": ownerAnswer.ID}, owner), 400)
	assertCode(t, call(t, h, "POST", "/api/community/posts/"+post.ID+"/resolve", map[string]any{"answerId": self.ID}, maya), 400)
	assertCode(t, call(t, h, "POST", "/api/community/posts/"+post.ID+"/resolve", map[string]any{"answerId": reply.ID}, outsider), 403)
	assertCode(t, call(t, h, "POST", "/api/community/posts/"+post.ID+"/resolve", map[string]any{"answerId": reply.ID}, jordan), 403)
	assertCode(t, call(t, h, "PATCH", "/api/records/"+post.ID, map[string]any{"status": "resolved", "data": map[string]any{"acceptedAnswerId": reply.ID}}, owner), 403)
	assertCode(t, call(t, h, "DELETE", "/api/records/"+reply.ID, nil, owner), 403)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- call(t, h, "POST", "/api/community/posts/"+post.ID+"/resolve", map[string]any{"answerId": reply.ID}, maya).Code
		}()
	}
	wg.Wait()
	close(codes)
	success, conflicts := 0, 0
	for code := range codes {
		if code == 200 {
			success++
		}
		if code == 409 {
			conflicts++
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("duplicate acceptance awarded twice")
	}
	summary := call(t, h, "GET", "/api/community/summary", nil, jordan)
	assertCode(t, summary, 200)
	var result domain.CommunitySummary
	_ = json.Unmarshal(summary.Body.Bytes(), &result)
	if result.Stats.Points != 10 || result.Stats.Resolved != 1 || result.Stats.CurrentStreak != 1 {
		t.Fatalf("wrong helper award %+v", result.Stats)
	}
	badgesResponse := call(t, h, "GET", "/api/community/badges?employeeIds=jordan,maya,unknown", nil, maya)
	assertCode(t, badgesResponse, 200)
	var badges map[string][]domain.Achievement
	_ = json.Unmarshal(badgesResponse.Body.Bytes(), &badges)
	found := false
	for _, badge := range badges["jordan"] {
		if !badge.Unlocked {
			t.Fatal("locked badge published")
		}
		if badge.ID == "resolve-1" {
			found = true
		}
	}
	if !found || badges["unknown"] != nil {
		t.Fatal("earned badges missing or unknown identity exposed")
	}
	assertCode(t, call(t, h, "GET", "/api/community/badges?employeeIds=jordan", nil, nil), 401)
	assertCode(t, call(t, h, "GET", "/api/community/badges?employeeIds="+strings.Repeat("maya,", 100)+"maya", nil, maya), 400)
	assertCode(t, call(t, h, "PATCH", "/api/community/answers/"+reply.ID, map[string]any{"body": "Changed after acceptance"}, jordan), 409)
	assertCode(t, call(t, h, "POST", "/api/community/posts/"+post.ID+"/answers", map[string]any{"body": "Late reply"}, outsider), 409)
	thread := call(t, h, "GET", "/api/community/posts/"+post.ID, nil, outsider)
	assertCode(t, thread, 200)
	if !bytes.Contains(thread.Body.Bytes(), []byte(reply.ID)) {
		t.Fatal("accepted solution not pinned")
	}
	generic := call(t, h, "GET", "/api/v2/records", nil, owner)
	if bytes.Contains(generic.Body.Bytes(), []byte(post.ID)) {
		t.Fatal("community bypasses dedicated projections")
	}
	assertCode(t, call(t, h, "POST", "/api/community/posts", map[string]any{"title": "Spoof", "body": "body", "type": "help", "employeeId": "emma", "points": 1000}, maya), 400)
	assertCode(t, call(t, h, "GET", "/api/community?cursor=bad", nil, maya), 400)
	assertCode(t, call(t, h, "GET", "/api/community", nil, nil), 401)
}
func TestCommunityRichTextCreateEditAndLegacyFallback(t *testing.T) {
	h := harness(t)
	maya := signIn(t, h, "maya@northstar.test")
	jordan := signIn(t, h, "jordan@northstar.test")
	doc := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Rich knowledge", "marks": []any{map[string]any{"type": "bold"}}}}}}}
	post := communityRecordResponse(t, h, "/api/community/posts", map[string]any{"title": "Formatting", "body": "spoof", "bodyDoc": doc, "type": "knowledge"}, maya)
	if domain.Text(post, "body") != "Rich knowledge" || post.Data["bodyDoc"] == nil {
		t.Fatal("rich body not persisted/canonicalized")
	}
	reply := communityRecordResponse(t, h, "/api/community/posts/"+post.ID+"/answers", map[string]any{"bodyDoc": doc}, jordan)
	if domain.Text(reply, "body") != "Rich knowledge" {
		t.Fatal("rich answer not canonicalized")
	}
	assertCode(t, call(t, h, "PATCH", "/api/community/answers/"+reply.ID, map[string]any{"bodyDoc": doc}, jordan), 200)
	thread := call(t, h, "GET", "/api/community/posts/"+post.ID, nil, jordan)
	if !bytes.Contains(thread.Body.Bytes(), []byte(`"bodyDoc"`)) {
		t.Fatal("rich document not returned")
	}
	w := call(t, h, "PATCH", "/api/community/posts/"+post.ID, map[string]any{"title": "Legacy edit", "body": "Plain replacement"}, maya)
	assertCode(t, w, 200)
	if bytes.Contains(w.Body.Bytes(), []byte(`"bodyDoc"`)) {
		t.Fatal("stale rich body survived plain replacement")
	}
	unsafe := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","attrs":{"onclick":"alert(1)"}}]}`)
	assertCode(t, call(t, h, "POST", "/api/community/posts/"+post.ID+"/answers", map[string]any{"body": "valid fallback", "bodyDoc": unsafe}, jordan), 400)
}
func TestProfilePhotoReplacementSharingAndNameFreeInvitation(t *testing.T) {
	h := harness(t)
	maya := signIn(t, h, "maya@northstar.test")
	jordan := signIn(t, h, "jordan@northstar.test")
	owner := signIn(t, h, "emma@northstar.test")
	var raw bytes.Buffer
	photo := image.NewNRGBA(image.Rect(0, 0, 800, 600))
	photo.Set(1, 1, color.NRGBA{R: 255, A: 255})
	_ = png.Encode(&raw, photo)
	upload := func() domain.File {
		w := uploadResource(t, h, maya, "photo.png", raw.Bytes(), map[string]string{"category": "avatar", "employeeId": "emma"})
		assertCode(t, w, 201)
		var f domain.File
		_ = json.Unmarshal(w.Body.Bytes(), &f)
		if f.EmployeeID != "maya" {
			t.Fatal("photo owner spoofed")
		}
		return f
	}
	first := upload()
	download := call(t, h, "GET", "/api/files/"+first.ID, nil, jordan)
	assertCode(t, download, 200)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(download.Body.Bytes()))
	if err != nil || cfg.Width > 512 || cfg.Height > 512 || !strings.HasPrefix(download.Header().Get("Content-Disposition"), "inline") {
		t.Fatal("photo not normalized for private display")
	}
	assertCode(t, call(t, h, "DELETE", "/api/files/"+first.ID, nil, jordan), 403)
	second := upload()
	assertCode(t, call(t, h, "GET", "/api/files/"+first.ID, nil, maya), 404)
	assertCode(t, call(t, h, "GET", "/api/files/"+second.ID, nil, jordan), 200)
	assertCode(t, uploadResource(t, h, maya, "bad.svg", []byte("<svg/>"), map[string]string{"category": "avatar"}), 400)
	assertCode(t, call(t, h, "PATCH", "/api/employees/maya", map[string]any{"avatarFileId": "spoof"}, maya), 400)
	assertCode(t, call(t, h, "DELETE", "/api/files/"+second.ID, nil, maya), 204)
	w := call(t, h, "POST", "/api/invitations", map[string]any{"email": "name-free@example.test", "role": "employee"}, owner)
	assertCode(t, w, 201)
	var invite struct {
		URL string `json:"inviteUrl"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &invite)
	token := strings.Split(strings.Split(invite.URL, "token=")[1], "&")[0]
	assertCode(t, call(t, h, "POST", "/api/auth/signup", map[string]any{"token": token, "name": "Name entered once", "password": "EmployeePassword2026!"}, nil), 201)
}
