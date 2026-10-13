package httpapi

import (
	"encoding/json"
	"net/http"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/seed"
	"northstar/backend/internal/store"
	"sync"
	"testing"
)

func TestPointAdjustmentsPermissionsLedgerSpendingAndConflicts(t *testing.T) {
	h := harness(t)
	owner := signIn(t, h, "emma@northstar.test")
	employee := signIn(t, h, "maya@northstar.test")
	hr := signIn(t, h, "mia@northstar.test")
	balance := func() domain.ShopWallet {
		w := call(t, h, "GET", "/api/points/maya", nil, owner)
		assertCode(t, w, 200)
		var v domain.ShopWallet
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		return v
	}
	before := balance()
	body := map[string]any{"mode": "add", "amount": 100, "reason": "Synthetic recognition", "key": "adjustment-test-key-001", "expectedBalance": before.Balance}
	assertCode(t, call(t, h, "POST", "/api/points/maya/adjustments", body, employee), 403)
	assertCode(t, call(t, h, "POST", "/api/points/maya/adjustments", body, hr), 403)
	assertCode(t, call(t, h, "GET", "/api/points/jordan", nil, employee), 403)
	assertCode(t, call(t, h, "GET", "/api/points/jordan/adjustments", nil, employee), 403)
	for range 2 {
		assertCode(t, call(t, h, "POST", "/api/points/maya/adjustments", body, owner), 200)
	}
	if v := balance(); v.Balance != before.Balance+100 || v.Earned != before.Earned || v.Spent != before.Spent || v.Adjusted != 100 {
		t.Fatalf("ledger wrong: %+v", v)
	}
	body["amount"] = 101
	assertCode(t, call(t, h, "POST", "/api/points/maya/adjustments", body, owner), 409)
	body["key"] = "adjustment-test-key-002"
	body["mode"] = "set"
	body["amount"] = 75
	assertCode(t, call(t, h, "POST", "/api/points/maya/adjustments", body, owner), 409)
	body["expectedBalance"] = balance().Balance
	assertCode(t, call(t, h, "POST", "/api/points/maya/adjustments", body, owner), 200)
	body["key"] = "adjustment-test-key-003"
	body["mode"] = "subtract"
	body["amount"] = 76
	body["expectedBalance"] = 75
	assertCode(t, call(t, h, "POST", "/api/points/maya/adjustments", body, owner), 409)
	if balance().Balance != 75 {
		t.Fatal("overdraw changed balance")
	}
	item := domain.ShopItem{Title: "Owner-funded reward", Kind: "voucher", Price: 70, Stock: 1, Active: true}
	w := call(t, h, "POST", "/api/shop/items", item, owner)
	assertCode(t, w, 201)
	_ = json.Unmarshal(w.Body.Bytes(), &item)
	assertCode(t, call(t, h, "POST", "/api/shop/purchases", map[string]string{"itemId": item.ID, "key": "synthetic-purchase-key-001"}, employee), 201)
	if balance().Balance != 5 {
		t.Fatal("adjusted points were not spendable")
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for n := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- call(t, h, "POST", "/api/points/maya/adjustments", map[string]any{"mode": "subtract", "amount": 4, "reason": "Synthetic correction", "key": []string{"concurrent-adjust-key-one", "concurrent-adjust-key-two"}[n], "expectedBalance": 5}, owner).Code
		}()
	}
	wg.Wait()
	close(codes)
	applied, conflicts := 0, 0
	for code := range codes {
		if code == 200 {
			applied++
		} else if code == 409 {
			conflicts++
		}
	}
	if applied != 1 || conflicts != 1 || balance().Balance != 1 {
		t.Fatal("concurrent stale balances were not rejected")
	}
	w = call(t, h, "GET", "/api/points/maya/adjustments?limit=1", nil, employee)
	assertCode(t, w, 200)
	var page store.PurchasePage
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("history not paginated")
	}
	assertCode(t, call(t, h, "POST", "/api/points/outsider/adjustments", map[string]any{"mode": "add", "amount": 1, "reason": "x", "key": "foreign-adjustment-key-001"}, owner), 404)
}

func TestWorkspaceStatusEnforcesTenantAccessAndOwnerRecovery(t *testing.T) {
	s, err := seed.Demo()
	if err != nil {
		t.Fatal(err)
	}
	s.Companies = append(s.Companies, domain.DefaultPolicy("other", "Other", "other"))
	s.Employees = append(s.Employees, domain.Employee{ID: "other", CompanyID: "other", Email: "other@example.test", Name: "Other", Role: "employee", PasswordHash: s.Employees[0].PasswordHash})
	h := New(store.NewMemory(s), Config{UploadDir: t.TempDir()})
	owner := signIn(t, h, "emma@northstar.test")
	employee := signIn(t, h, "maya@northstar.test")
	hr := signIn(t, h, "mia@northstar.test")
	other := signIn(t, h, "other@example.test")
	save := func(mode string, cookie *http.Cookie) int {
		return call(t, h, "PATCH", "/api/workspace/status", domain.WorkspaceStatus{Mode: mode, Message: "Synthetic service message", MessageTH: "ข้อความทดสอบ"}, cookie).Code
	}
	if save("maintenance", employee) != 403 || save("read-only", hr) != 403 {
		t.Fatal("employee changed status")
	}
	if save("unknown", owner) != 400 {
		t.Fatal("invalid status accepted")
	}
	if save("maintenance", owner) != 200 {
		t.Fatal("owner cannot pause service")
	}
	for _, path := range []string{"/api/bootstrap", "/api/v2/bootstrap", "/api/v2/records", "/api/shop/wallet", "/api/files/missing"} {
		w := call(t, h, "GET", path, nil, employee)
		assertCode(t, w, 503)
		if w.Header().Get("Retry-After") != "30" || !json.Valid(w.Body.Bytes()) {
			t.Fatal("invalid maintenance response")
		}
	}
	assertCode(t, call(t, h, "GET", "/api/workspace/status", nil, employee), 200)
	assertCode(t, call(t, h, "GET", "/api/v2/bootstrap", nil, other), 200)
	assertCode(t, call(t, h, "GET", "/api/v2/bootstrap", nil, owner), 200)
	assertCode(t, call(t, h, "GET", "/healthz", nil, nil), 200)
	if save("read-only", owner) != 200 {
		t.Fatal("owner cannot restore reads")
	}
	assertCode(t, call(t, h, "GET", "/api/v2/bootstrap", nil, employee), 200)
	assertCode(t, call(t, h, "POST", "/api/shop/purchases", map[string]any{}, employee), 503)
	if save("degraded", owner) != 200 {
		t.Fatal("degraded status failed")
	}
	assertCode(t, call(t, h, "POST", "/api/shop/purchases", map[string]any{}, employee), 400)
	if save("operational", owner) != 200 {
		t.Fatal("owner cannot restore service")
	}
	assertCode(t, call(t, h, "GET", "/api/v2/bootstrap", nil, employee), 200)
	assertCode(t, call(t, h, "POST", "/api/auth/logout", nil, employee), 204)
}
