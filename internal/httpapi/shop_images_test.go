package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"northstar/backend/internal/domain"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommunityDraftCleanupAndShopLedgerRetention(t *testing.T) {
	a, _, repo, _ := productionHarness(t)
	ctx := domain.WithTenant(context.Background(), "default")
	err := repo.Update(ctx, func(s *domain.State) error {
		s.Files = append(s.Files, domain.File{ID: "old-draft", EmployeeID: "maya", CompanyID: "default", Category: "community-image", CreatedAt: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)}, domain.File{ID: "fresh-draft", EmployeeID: "jordan", CompanyID: "default", Category: "community-image", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}, domain.File{ID: "published-image", EmployeeID: "maya", CompanyID: "default", Category: "community-image", RecordID: "knowledge-history", CreatedAt: time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)})
		e := s.Employee("maya")
		e.Status = "offboarded"
		e.RetainUntil = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
		e.Decoration = domain.Decoration{Border: "pulse"}
		s.Operations = append(s.Operations, domain.Operation{ID: "retained-spend", CompanyID: "default", EmployeeID: "maya", Kind: "shop-purchase", Status: "fulfilled", CreatedAt: stamp(), Data: map[string]any{"price": float64(10), "key": "old-key", "delivery": "private voucher details"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.applyRetention(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	s, err := repo.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, f := range s.Files {
		kept[f.ID] = true
	}
	if kept["old-draft"] || !kept["fresh-draft"] || !kept["published-image"] {
		t.Fatal("image lifecycle cleanup mismatch")
	}
	if s.Employee("maya").Decoration.Border != "" {
		t.Fatal("decoration survived anonymization")
	}
	found := false
	for _, op := range s.Operations {
		if op.ID == "retained-spend" {
			found = true
			if op.Data["key"] != nil || op.Data["delivery"] != nil || op.Data["price"] != float64(10) {
				t.Fatal("spend ledger retained private delivery data or lost accounting")
			}
		}
	}
	if !found {
		t.Fatal("point ledger disappeared")
	}
}

func earnPoints(t *testing.T, h http.Handler, author, helper *http.Cookie, count int) {
	t.Helper()
	for range count {
		p := communityRecordResponse(t, h, "/api/community/posts", map[string]any{"title": "Synthetic question", "type": "help", "body": "Please help"}, author)
		a := communityRecordResponse(t, h, "/api/community/posts/"+p.ID+"/answers", map[string]any{"body": "Synthetic solution"}, helper)
		assertCode(t, call(t, h, "POST", "/api/community/posts/"+p.ID+"/resolve", map[string]any{"answerId": a.ID}, author), 200)
	}
}
func TestShopAtomicSpendingIdempotencyFulfillmentAndEquip(t *testing.T) {
	h := harness(t)
	owner := signIn(t, h, "emma@northstar.test")
	maya := signIn(t, h, "maya@northstar.test")
	helper := signIn(t, h, "jordan@northstar.test")
	earnPoints(t, h, maya, helper, 5)
	create := func(kind, effect string, price, stock int) domain.ShopItem {
		t.Helper()
		v := domain.ShopItem{Title: "Synthetic " + kind, Description: "Test only", Kind: kind, Effect: effect, Price: price, Stock: stock, Active: true}
		assertCode(t, call(t, h, "POST", "/api/shop/items", v, maya), 403)
		w := call(t, h, "POST", "/api/shop/items", v, owner)
		assertCode(t, w, 201)
		var saved domain.ShopItem
		_ = json.Unmarshal(w.Body.Bytes(), &saved)
		return saved
	}
	border := create("border", "pulse", 10, -1)
	key := "idempotent-purchase-0001"
	buy := map[string]any{"itemId": border.ID, "key": key}
	w := call(t, h, "POST", "/api/shop/purchases", buy, helper)
	assertCode(t, w, 201)
	var purchased domain.Operation
	_ = json.Unmarshal(w.Body.Bytes(), &purchased)
	assertCode(t, call(t, h, "POST", "/api/shop/purchases", buy, helper), 200)
	assertCode(t, call(t, h, "POST", "/api/shop/purchases", map[string]any{"itemId": border.ID, "key": "another-purchase-001"}, helper), 409)
	assertCode(t, call(t, h, "POST", "/api/shop/equip", map[string]any{"purchaseId": purchased.ID, "slot": "border"}, maya), 403)
	assertCode(t, call(t, h, "POST", "/api/shop/equip", map[string]any{"purchaseId": purchased.ID, "slot": "border"}, helper), 204)
	assertCode(t, call(t, h, "PATCH", "/api/employees/jordan", map[string]any{"decoration": map[string]any{"nameColor": "gold"}}, helper), 400)
	profile := call(t, h, "GET", "/api/v2/bootstrap", nil, helper)
	if !bytes.Contains(profile.Body.Bytes(), []byte(`"border":"pulse"`)) {
		t.Fatal("equipped decoration missing")
	}
	coupon := create("voucher", "", 30, 2)
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"concurrent-purchase-0001", "concurrent-purchase-0002"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			codes <- call(t, h, "POST", "/api/shop/purchases", map[string]any{"itemId": coupon.ID, "key": key}, helper).Code
		}(key)
	}
	wg.Wait()
	close(codes)
	ok, conflict := 0, 0
	for c := range codes {
		if c == 201 {
			ok++
		}
		if c == 409 {
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("overspending allowed: %d/%d", ok, conflict)
	}
	w = call(t, h, "GET", "/api/shop/wallet", nil, helper)
	assertCode(t, w, 200)
	var wallet domain.ShopWallet
	_ = json.Unmarshal(w.Body.Bytes(), &wallet)
	if wallet.Earned != 50 || wallet.Spent != 40 || wallet.Balance != 10 {
		t.Fatalf("wallet mismatch %+v", wallet)
	}
	queue := call(t, h, "GET", "/api/shop/purchases?queue=true", nil, owner)
	assertCode(t, queue, 200)
	var page struct {
		Items []domain.Operation `json:"items"`
	}
	_ = json.Unmarshal(queue.Body.Bytes(), &page)
	if len(page.Items) != 1 {
		t.Fatal("fulfillment queue missing")
	}
	reward := page.Items[0]
	assertCode(t, call(t, h, "PATCH", "/api/shop/purchases/"+reward.ID, map[string]any{"delivery": "SYNTHETIC-PRIVATE-CODE"}, helper), 403)
	assertCode(t, call(t, h, "PATCH", "/api/shop/purchases/"+reward.ID, map[string]any{"delivery": "SYNTHETIC-PRIVATE-CODE"}, owner), 204)
	assertCode(t, call(t, h, "PATCH", "/api/shop/purchases/"+reward.ID, map[string]any{"delivery": "changed"}, owner), 409)
	private := call(t, h, "GET", "/api/shop/purchases?queue=true", nil, maya)
	if bytes.Contains(private.Body.Bytes(), []byte("SYNTHETIC-PRIVATE-CODE")) || bytes.Contains(private.Body.Bytes(), []byte(reward.ID)) {
		t.Fatal("another employee purchase exposed")
	}
	own := call(t, h, "GET", "/api/shop/purchases", nil, helper)
	assertCode(t, call(t, h, "GET", "/api/shop/purchases/"+reward.ID, nil, maya), 404)
	assertCode(t, call(t, h, "GET", "/api/shop/purchases/"+reward.ID, nil, helper), 200)
	if !bytes.Contains(own.Body.Bytes(), []byte("SYNTHETIC-PRIVATE-CODE")) {
		t.Fatal("delivery not available to buyer")
	}
	assertCode(t, call(t, h, "PATCH", "/api/records/"+border.ID, map[string]any{"data": map[string]any{"price": 0}}, owner), 403)
	generic := call(t, h, "GET", "/api/v2/records", nil, owner)
	if bytes.Contains(generic.Body.Bytes(), []byte(border.ID)) {
		t.Fatal("shop bypasses dedicated API")
	}
	notices := call(t, h, "GET", "/api/notifications?limit=2", nil, helper)
	assertCode(t, notices, 200)
	if !bytes.Contains(notices.Body.Bytes(), []byte(`"unreadCount":6`)) {
		t.Fatal("unread count must include all pages: " + notices.Body.String())
	}
	assertCode(t, call(t, h, "PATCH", "/api/notifications/all", nil, helper), 204)
	notices = call(t, h, "GET", "/api/notifications?limit=2", nil, helper)
	if !bytes.Contains(notices.Body.Bytes(), []byte(`"unreadCount":0`)) {
		t.Fatal("mark all read did not persist")
	}
	lastStock := create("coupon", "", 1, 1)
	stockCodes := make(chan int, 2)
	for _, key := range []string{"last-stock-purchase-0001", "last-stock-purchase-0002"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			stockCodes <- call(t, h, "POST", "/api/shop/purchases", map[string]any{"itemId": lastStock.ID, "key": key}, helper).Code
		}(key)
	}
	wg.Wait()
	close(stockCodes)
	sold := 0
	for code := range stockCodes {
		if code == 201 {
			sold++
		} else if code != 409 {
			t.Fatalf("unexpected stock status %d", code)
		}
	}
	if sold != 1 {
		t.Fatal("last reward sold twice")
	}
	border.Title = "Hidden pulse"
	border.Price = 99
	border.Stock = 4
	border.Active = false
	assertCode(t, call(t, h, "PATCH", "/api/shop/items/"+border.ID, border, owner), 200)
	visibleShop := call(t, h, "GET", "/api/shop", nil, helper)
	if bytes.Contains(visibleShop.Body.Bytes(), []byte(border.ID)) {
		t.Fatal("hidden reward exposed in employee catalog")
	}
	allShop := call(t, h, "GET", "/api/shop?all=true", nil, owner)
	if !bytes.Contains(allShop.Body.Bytes(), []byte("Hidden pulse")) {
		t.Fatal("owner cannot maintain hidden reward")
	}
	border.Effect = "glow"
	assertCode(t, call(t, h, "PATCH", "/api/shop/items/"+border.ID, border, owner), 400)
	updatedWallet := call(t, h, "GET", "/api/shop/wallet", nil, helper)
	_ = json.Unmarshal(updatedWallet.Body.Bytes(), &wallet)
	if wallet.Spent != 41 {
		t.Fatal("catalog edit changed a purchase price snapshot")
	}
}
func TestCommunityImagePublicationAndPrivacy(t *testing.T) {
	h := harness(t)
	maya := signIn(t, h, "maya@northstar.test")
	jordan := signIn(t, h, "jordan@northstar.test")
	owner := signIn(t, h, "emma@northstar.test")
	var raw bytes.Buffer
	_ = png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 800, 600)))
	w := uploadResource(t, h, maya, "synthetic.png", raw.Bytes(), map[string]string{"category": "community-image", "employeeId": "emma"})
	assertCode(t, w, 201)
	var f domain.File
	_ = json.Unmarshal(w.Body.Bytes(), &f)
	if f.EmployeeID != "maya" {
		t.Fatal("image owner spoofed")
	}
	assertCode(t, call(t, h, "GET", "/api/files/"+f.ID, nil, jordan), 403)
	doc := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "image", "attrs": map[string]any{"fileId": f.ID, "alt": "Synthetic screenshot"}}}}
	assertCode(t, call(t, h, "POST", "/api/community/posts", map[string]any{"title": "Borrowed draft", "type": "knowledge", "bodyDoc": doc}, jordan), 403)
	post := communityRecordResponse(t, h, "/api/community/posts", map[string]any{"title": "Image post", "type": "knowledge", "bodyDoc": doc}, maya)
	if domain.Text(post, "body") != "Synthetic screenshot" {
		t.Fatal("image-only text fallback missing")
	}
	imageResponse := call(t, h, "GET", "/api/files/"+f.ID, nil, jordan)
	assertCode(t, imageResponse, 200)
	if !strings.HasPrefix(imageResponse.Header().Get("Content-Disposition"), "inline") {
		t.Fatal("image not inline")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(imageResponse.Body.Bytes()))
	if err != nil || config.Width != 800 {
		t.Fatal("image was cropped like a profile photo")
	}
	assertCode(t, call(t, h, "DELETE", "/api/files/"+f.ID, nil, maya), 403)
	assertCode(t, call(t, h, "DELETE", "/api/files/"+f.ID, nil, owner), 403)
	assertCode(t, uploadResource(t, h, maya, "fake.png", []byte("<svg onload='alert(1)'></svg>"), map[string]string{"category": "community-image"}), 400)
	malicious := json.RawMessage(`{"type":"doc","content":[{"type":"image","attrs":{"fileId":"missing","src":"https://evil.test/pixel"}}]}`)
	assertCode(t, call(t, h, "POST", "/api/community/posts/"+post.ID+"/answers", map[string]any{"bodyDoc": malicious}, jordan), 400)
	visible := call(t, h, "GET", "/api/v2/files", nil, owner)
	if bytes.Contains(visible.Body.Bytes(), []byte(f.ID)) {
		t.Fatal("community images polluted personal drive")
	}
}
