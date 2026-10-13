package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

type Decoration struct {
	Border    string `json:"border,omitempty"`
	NameColor string `json:"nameColor,omitempty"`
	NameStyle string `json:"nameStyle,omitempty"`
}
type ShopItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Effect      string `json:"effect"`
	Price       int    `json:"price"`
	Stock       int    `json:"stock"`
	Active      bool   `json:"active"`
}
type ShopWallet struct {
	OwnedDecorations []string `json:"ownedDecorations"`
	Earned           int      `json:"earned"`
	Spent            int      `json:"spent"`
	Adjusted         int      `json:"adjusted"`
	Balance          int      `json:"balance"`
}

func DecorationKind(kind string) bool {
	return Contains([]string{"border", "name-color", "name-style"}, kind)
}
func ValidateShopItem(v ShopItem) error {
	if strings.TrimSpace(v.Title) == "" || utf8.RuneCountInString(v.Title) > 120 || utf8.RuneCountInString(v.Description) > 2000 || v.Price < 1 || v.Price > 1000000 || v.Stock < -1 || v.Stock > 1000000 {
		return errors.New("enter a reward title, price 1-1000000 and stock -1 or higher")
	}
	if !Contains([]string{"coupon", "voucher", "discount", "border", "name-color", "name-style"}, v.Kind) {
		return errors.New("choose a valid reward type")
	}
	allowed := map[string][]string{"border": {"blue", "gold", "rainbow", "pulse", "glow"}, "name-color": {"blue", "purple", "green", "rose", "gold"}, "name-style": {"pixel", "bubble", "medieval", "bold", "italic", "glow", "gradient"}}
	if DecorationKind(v.Kind) && !Contains(allowed[v.Kind], v.Effect) || !DecorationKind(v.Kind) && v.Effect != "" {
		return errors.New("choose a valid decoration effect")
	}
	return nil
}
func ShopItemRecord(v ShopItem, u Employee, created string) Record {
	b, _ := json.Marshal(v)
	var data map[string]any
	_ = json.Unmarshal(b, &data)
	status := "archived"
	if v.Active {
		status = "active"
	}
	return Record{ID: v.ID, CompanyID: CompanyID(u.CompanyID), EmployeeID: u.ID, Kind: "shop-item", Title: strings.TrimSpace(v.Title), Status: status, CreatedAt: created, Data: data}
}
func ShopRecordItem(r Record) ShopItem {
	b, _ := json.Marshal(r.Data)
	var item ShopItem
	_ = json.Unmarshal(b, &item)
	item.ID = r.ID
	item.Title = r.Title
	item.Active = r.Status == "active"
	return item
}
func CommunityEarned(s *State, id string) int {
	answers := map[string]Record{}
	for _, r := range s.Records {
		if r.Kind == "community-answer" && r.EmployeeID == id {
			answers[r.ID] = r
		}
	}
	earned := 0
	for _, r := range s.Records {
		if r.Kind != "community-post" || r.Status != "resolved" || Text(r, "type") != "help" {
			continue
		}
		a, ok := answers[Text(r, "acceptedAnswerId")]
		if ok && Text(a, "postId") == r.ID && a.EmployeeID != r.EmployeeID && a.EmployeeID != Text(r, "acceptedBy") {
			earned += 10
		}
	}
	return earned
}
func CommunityWallet(s *State, id string) ShopWallet {
	w := ShopWallet{Earned: CommunityEarned(s, id), OwnedDecorations: []string{}}
	seen := map[string]bool{}
	for _, op := range s.Operations {
		if op.Kind == "points-adjustment" && op.EmployeeID == id {
			value, _ := op.Data["delta"].(float64)
			w.Adjusted += int(value)
		}
		if op.Kind == "shop-purchase" && op.EmployeeID == id {
			kind, _ := op.Data["kind"].(string)
			effect, _ := op.Data["effect"].(string)
			key := kind + ":" + effect
			if DecorationKind(kind) && !seen[key] {
				w.OwnedDecorations = append(w.OwnedDecorations, key)
				seen[key] = true
			}
			value, _ := op.Data["price"].(float64)
			w.Spent += int(value)
		}
	}
	w.Balance = w.Earned + w.Adjusted - w.Spent
	return w
}
