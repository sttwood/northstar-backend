package httpapi

import (
	"encoding/json"
	"northstar/backend/internal/domain"
	"sync"
	"testing"
)

func TestHolidayImportOwnerPreviewAtomicAndConcurrent(t *testing.T) {
	h := harness(t)
	owner := signIn(t, h, "emma@northstar.test")
	employee := signIn(t, h, "maya@northstar.test")
	hr := signIn(t, h, "mia@northstar.test")
	input := domain.HolidayCalendarImport{Year: 2026, Months: []domain.HolidayImportMonth{{ID: 1, Holidays: []domain.HolidayImportDay{{Date: 1, Day: "th", NameEN: "Synthetic imported holiday", NameTH: "วันหยุดตัวอย่าง"}}}}}
	count := func() int {
		w := call(t, h, "GET", "/api/v2/records?kind=holiday", nil, owner)
		assertCode(t, w, 200)
		var p struct {
			Items []domain.Record `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &p)
		return len(p.Items)
	}
	before := count()
	assertCode(t, call(t, h, "POST", "/api/holidays/import/preview", input, employee), 403)
	assertCode(t, call(t, h, "POST", "/api/holidays/import", input, hr), 403)
	preview := call(t, h, "POST", "/api/holidays/import/preview", input, owner)
	assertCode(t, preview, 200)
	if count() != before {
		t.Fatal("preview wrote holidays")
	}
	var result domain.HolidayImportResult
	_ = json.Unmarshal(preview.Body.Bytes(), &result)
	if result.Created != 1 {
		t.Fatal("preview lost holiday")
	}
	invalid := input
	invalid.Months = []domain.HolidayImportMonth{{ID: 1, Holidays: []domain.HolidayImportDay{{Date: 1, Day: "th", NameEN: "Valid staged holiday"}, {Date: 32, NameEN: "Invalid later row"}}}}
	assertCode(t, call(t, h, "POST", "/api/holidays/import", invalid, owner), 400)
	if count() != before {
		t.Fatal("invalid import partially wrote")
	}
	var wg sync.WaitGroup
	created := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := call(t, h, "POST", "/api/holidays/import", input, owner)
			assertCode(t, w, 200)
			var out domain.HolidayImportResult
			_ = json.Unmarshal(w.Body.Bytes(), &out)
			created <- out.Created
		}()
	}
	wg.Wait()
	close(created)
	total := 0
	for n := range created {
		total += n
	}
	if total != 1 || count() != before+1 {
		t.Fatal("concurrent import duplicated holidays")
	}
	assertCode(t, call(t, h, "POST", "/api/holidays/import", map[string]any{"year": 2026, "month": []any{}, "employeeId": "maya"}, owner), 400)
}
func TestNewFontRewardsEquipAndUnequip(t *testing.T) {
	h := harness(t)
	owner := signIn(t, h, "emma@northstar.test")
	maya := signIn(t, h, "maya@northstar.test")
	helper := signIn(t, h, "jordan@northstar.test")
	earnPoints(t, h, maya, helper, 1)
	for _, style := range []string{"pixel", "bubble", "medieval"} {
		w := call(t, h, "POST", "/api/shop/items", domain.ShopItem{Title: style, Kind: "name-style", Effect: style, Price: 1, Stock: -1, Active: true}, owner)
		assertCode(t, w, 201)
		var reward domain.ShopItem
		_ = json.Unmarshal(w.Body.Bytes(), &reward)
		w = call(t, h, "POST", "/api/shop/purchases", map[string]any{"itemId": reward.ID, "key": "font-purchase-0000-" + style}, helper)
		assertCode(t, w, 201)
		var purchase domain.Operation
		_ = json.Unmarshal(w.Body.Bytes(), &purchase)
		assertCode(t, call(t, h, "POST", "/api/shop/equip", map[string]any{"slot": "name-style", "purchaseId": purchase.ID}, helper), 204)
		profile := call(t, h, "GET", "/api/v2/bootstrap", nil, helper)
		var data struct {
			Me domain.Employee `json:"me"`
		}
		_ = json.Unmarshal(profile.Body.Bytes(), &data)
		if data.Me.Decoration.NameStyle != style {
			t.Fatal("font not equipped")
		}
		assertCode(t, call(t, h, "POST", "/api/shop/equip", map[string]any{"slot": "name-style", "purchaseId": ""}, helper), 204)
		profile = call(t, h, "GET", "/api/v2/bootstrap", nil, helper)
		data.Me = domain.Employee{}
		_ = json.Unmarshal(profile.Body.Bytes(), &data)
		if data.Me.Decoration.NameStyle != "" {
			t.Fatal("font not unequipped")
		}
	}
}
