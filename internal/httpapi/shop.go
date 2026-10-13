package httpapi

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/store"
	"strings"
	"unicode/utf8"
)

func communityNotice(s *domain.State, employee, title, post string) {
	e := s.Employee(employee)
	if e == nil || !domain.Active(*e) {
		return
	}
	s.Operations = append(s.Operations, domain.Operation{ID: id(), CompanyID: domain.CompanyID(s.Employee(employee).CompanyID), EmployeeID: employee, Kind: "notification", Status: "unread", CreatedAt: stamp(), Data: map[string]any{"title": title, "recordId": post, "kind": "community-post"}})
}
func (a *API) shopRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/shop/purchases/{id}", a.auth(endpoint(a.shopPurchase)))
	routes := map[string]func(http.ResponseWriter, *http.Request) error{"GET /api/shop": a.shopList, "POST /api/shop/items": a.shopSave, "PATCH /api/shop/items/{id}": a.shopSave, "GET /api/shop/wallet": a.shopWallet, "GET /api/shop/purchases": a.shopPurchases, "POST /api/shop/purchases": a.shopBuy, "PATCH /api/shop/purchases/{id}": a.shopFulfill, "POST /api/shop/equip": a.shopEquip}
	for path, fn := range routes {
		mux.Handle(path, a.auth(endpoint(fn)))
	}
}
func (a *API) shopPurchase(w http.ResponseWriter, r *http.Request) error {
	purchase, err := a.store.(store.ShopRepository).PurchaseByID(r.Context(), current(r), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || err.Error() == "purchase not found" {
			return fail(404, "purchase not found")
		}
		return err
	}
	respond(w, 200, purchase)
	return nil
}
func (a *API) shopList(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	f := store.PageFilter{Kind: "shop-item", Limit: communityLimit(r), Cursor: r.URL.Query().Get("cursor"), Search: r.URL.Query().Get("search"), Status: "active"}
	if len(f.Search) > 200 || len(f.Cursor) > 100 {
		return fail(400, "invalid shop filter")
	}
	if u.Role == "owner" && r.URL.Query().Get("all") == "true" {
		f.Status = ""
	}
	page, err := a.store.(store.ShopRepository).ShopPage(r.Context(), u, f)
	if err != nil {
		return err
	}
	respond(w, 200, page)
	return nil
}
func (a *API) shopWallet(w http.ResponseWriter, r *http.Request) error {
	wallet, err := a.store.(store.ShopRepository).ShopWallet(r.Context(), current(r))
	if err != nil {
		return err
	}
	respond(w, 200, wallet)
	return nil
}
func (a *API) shopPurchases(w http.ResponseWriter, r *http.Request) error {
	if len(r.URL.Query().Get("cursor")) > 100 {
		return fail(400, "invalid shop filter")
	}
	u := current(r)
	ownerQueue := u.Role == "owner" && r.URL.Query().Get("queue") == "true"
	page, err := a.store.(store.ShopRepository).ShopPurchases(r.Context(), u, ownerQueue, r.URL.Query().Get("cursor"), communityLimit(r))
	if err != nil {
		return err
	}
	respond(w, 200, page)
	return nil
}
func (a *API) shopSave(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "only the owner may manage rewards")
	}
	var v domain.ShopItem
	if err := decode(w, r, &v); err != nil {
		return err
	}
	if err := domain.ValidateShopItem(v); err != nil {
		return fail(400, err.Error())
	}
	target := r.PathValue("id")
	v.ID = target
	if target == "" {
		v.ID = id()
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := communityActor(s, u); err != nil {
			return err
		}
		if s.Employee(u.ID).Role != "owner" {
			return fail(403, "only the owner may manage rewards")
		}
		if target != "" {
			for i, old := range s.Records {
				if old.ID == target && old.Kind == "shop-item" {
					if domain.ShopRecordItem(old).Kind != v.Kind || domain.ShopRecordItem(old).Effect != v.Effect {
						return fail(400, "reward type and effect cannot be changed")
					}
					s.Records[i] = domain.ShopItemRecord(v, u, old.CreatedAt)
					audit(s, u, "updated shop reward", v.ID)
					return nil
				}
			}
			return fail(404, "reward not found")
		}
		s.Records = append(s.Records, domain.ShopItemRecord(v, u, stamp()))
		audit(s, u, "created shop reward", v.ID)
		return nil
	})
	if err != nil {
		return err
	}
	code := 200
	if target == "" {
		code = 201
	}
	respond(w, code, v)
	return nil
}
func (a *API) shopBuy(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	var body struct {
		ItemID string `json:"itemId"`
		Key    string `json:"key"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if len(body.Key) < 16 || len(body.Key) > 100 || strings.TrimSpace(body.Key) != body.Key {
		return fail(400, "a purchase key is required")
	}
	var result domain.Operation
	replayed := false
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := communityActor(s, u); err != nil {
			return err
		}
		for _, op := range s.Operations {
			if op.Kind == "shop-purchase" && op.EmployeeID == u.ID && op.Data["key"] == body.Key {
				if op.Data["itemId"] != body.ItemID {
					return fail(409, "purchase key already used")
				}
				result = op
				replayed = true
				return nil
			}
		}
		for i := range s.Records {
			record := &s.Records[i]
			if record.ID != body.ItemID || record.Kind != "shop-item" {
				continue
			}
			item := domain.ShopRecordItem(*record)
			if !item.Active || item.Stock == 0 {
				return fail(409, "reward is unavailable")
			}
			if domain.CommunityWallet(s, u.ID).Balance < item.Price {
				return fail(409, "not enough points")
			}
			if domain.DecorationKind(item.Kind) {
				for _, op := range s.Operations {
					if op.Kind == "shop-purchase" && op.EmployeeID == u.ID && op.Data["kind"] == item.Kind && op.Data["effect"] == item.Effect {
						return fail(409, "you already own this decoration")
					}
				}
			}
			status := "pending"
			if domain.DecorationKind(item.Kind) {
				status = "fulfilled"
			}
			result = domain.Operation{ID: id(), CompanyID: domain.CompanyID(u.CompanyID), EmployeeID: u.ID, Kind: "shop-purchase", Status: status, CreatedAt: stamp(), Data: map[string]any{"key": body.Key, "itemId": item.ID, "title": item.Title, "description": item.Description, "kind": item.Kind, "effect": item.Effect, "price": float64(item.Price)}}
			if item.Stock > 0 {
				record.Data["stock"] = float64(item.Stock - 1)
			}
			s.Operations = append(s.Operations, result)
			audit(s, u, "redeemed shop reward", result.ID)
			if status == "pending" {
				for _, e := range s.Employees {
					if e.Role == "owner" && domain.Active(e) && e.ID != u.ID {
						s.Operations = append(s.Operations, domain.Operation{ID: id(), CompanyID: domain.CompanyID(u.CompanyID), EmployeeID: e.ID, Kind: "notification", Status: "unread", CreatedAt: stamp(), Data: map[string]any{"title": "Reward awaiting fulfillment", "kind": "shop-purchase", "recordId": result.ID}})
					}
				}
			}
			return nil
		}
		return fail(404, "reward not found")
	})
	if err != nil {
		return err
	}
	code := 201
	if replayed {
		code = 200
	}
	respond(w, code, result)
	return nil
}
func (a *API) shopFulfill(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "only the owner may fulfill rewards")
	}
	var body struct {
		Delivery string `json:"delivery"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Delivery) == "" || utf8.RuneCountInString(body.Delivery) > 2000 {
		return fail(400, "enter delivery details up to 2000 characters")
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := communityActor(s, u); err != nil {
			return err
		}
		if s.Employee(u.ID).Role != "owner" {
			return fail(403, "only the owner may fulfill rewards")
		}
		for i := range s.Operations {
			op := &s.Operations[i]
			if op.ID == r.PathValue("id") && op.Kind == "shop-purchase" {
				if op.Status != "pending" {
					return fail(409, "reward already fulfilled")
				}
				op.Status = "fulfilled"
				op.Data["delivery"] = strings.TrimSpace(body.Delivery)
				op.Data["fulfilledAt"] = stamp()
				employee, target := op.EmployeeID, op.ID
				audit(s, u, "fulfilled shop reward", target)
				s.Operations = append(s.Operations, domain.Operation{ID: id(), CompanyID: domain.CompanyID(u.CompanyID), EmployeeID: employee, Kind: "notification", Status: "unread", CreatedAt: stamp(), Data: map[string]any{"title": "Your reward is ready", "kind": "shop-purchase", "recordId": target}})
				return nil
			}
		}
		return fail(404, "purchase not found")
	})
	if err != nil {
		return err
	}
	respond(w, 204, nil)
	return nil
}
func (a *API) shopEquip(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	var body struct {
		PurchaseID string `json:"purchaseId"`
		Slot       string `json:"slot"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if !domain.DecorationKind(body.Slot) {
		return fail(400, "choose a decoration slot")
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := communityActor(s, u); err != nil {
			return err
		}
		effect := ""
		if body.PurchaseID != "" {
			found := false
			for _, op := range s.Operations {
				if op.ID == body.PurchaseID && op.Kind == "shop-purchase" && op.EmployeeID == u.ID && op.Status == "fulfilled" && op.Data["kind"] == body.Slot {
					effect, _ = op.Data["effect"].(string)
					found = true
					break
				}
			}
			if !found {
				return fail(403, "you do not own this decoration")
			}
		}
		e := s.Employee(u.ID)
		switch body.Slot {
		case "border":
			e.Decoration.Border = effect
		case "name-color":
			e.Decoration.NameColor = effect
		case "name-style":
			e.Decoration.NameStyle = effect
		}
		audit(s, u, "equipped profile decoration", u.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 204, nil)
	return nil
}
