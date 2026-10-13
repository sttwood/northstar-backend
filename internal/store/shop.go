package store

import (
	"context"
	"encoding/json"
	"errors"
	"northstar/backend/internal/domain"
	"sort"
	"strings"
)

type ShopPage struct {
	Items      []domain.ShopItem `json:"items"`
	NextCursor string            `json:"nextCursor"`
}
type ShopPurchase struct {
	domain.Operation
	EmployeeName string `json:"employeeName,omitempty"`
}
type PurchasePage struct {
	Items      []ShopPurchase `json:"items"`
	NextCursor string         `json:"nextCursor"`
}
type ShopRepository interface {
	PurchaseByID(context.Context, domain.Employee, string) (ShopPurchase, error)
	ShopPage(context.Context, domain.Employee, PageFilter) (ShopPage, error)
	ShopWallet(context.Context, domain.Employee) (domain.ShopWallet, error)
	ShopPurchases(context.Context, domain.Employee, bool, string, int) (PurchasePage, error)
	PointAdjustments(context.Context, domain.Employee, string, string, int) (PurchasePage, error)
}

func (p *Postgres) ShopPage(ctx context.Context, u domain.Employee, f PageFilter) (ShopPage, error) {
	out := ShopPage{Items: []domain.ShopItem{}}
	rows, err := p.pool.Query(ctx, `SELECT payload FROM records WHERE company_id=$1 AND kind='shop-item' AND id>$2 AND ($3='' OR payload->>'status'=$3) AND position(lower($4) in lower(payload->>'title'))>0 ORDER BY id LIMIT $5`, domain.CompanyID(u.CompanyID), f.Cursor, f.Status, f.Search, f.Limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var r domain.Record
		if err = rows.Scan(&b); err != nil {
			return out, err
		}
		if err = json.Unmarshal(b, &r); err != nil {
			return out, err
		}
		if len(out.Items) == f.Limit {
			out.NextCursor = out.Items[len(out.Items)-1].ID
			break
		}
		out.Items = append(out.Items, domain.ShopRecordItem(r))
	}
	return out, rows.Err()
}
func (m *Memory) ShopPage(ctx context.Context, u domain.Employee, f PageFilter) (ShopPage, error) {
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	out := ShopPage{Items: []domain.ShopItem{}}
	if err != nil {
		return out, err
	}
	for _, r := range s.Records {
		if r.Kind == "shop-item" && r.ID > f.Cursor && (f.Status == "" || r.Status == f.Status) && strings.Contains(strings.ToLower(r.Title), strings.ToLower(f.Search)) {
			out.Items = append(out.Items, domain.ShopRecordItem(r))
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].ID < out.Items[j].ID })
	if len(out.Items) > f.Limit {
		out.Items = out.Items[:f.Limit]
		out.NextCursor = out.Items[len(out.Items)-1].ID
	}
	return out, nil
}
func (p *Postgres) ShopWallet(ctx context.Context, u domain.Employee) (domain.ShopWallet, error) {
	w := domain.ShopWallet{OwnedDecorations: []string{}}
	var solved int
	err := p.pool.QueryRow(ctx, "SELECT COUNT(*)"+acceptedCommunityJoin+" AND a.employee_id=$2", domain.CompanyID(u.CompanyID), u.ID).Scan(&solved)
	if err != nil {
		return w, err
	}
	w.Earned = solved * 10
	err = p.pool.QueryRow(ctx, `SELECT COALESCE(SUM((payload->'data'->>'price')::integer),0) FROM operations WHERE company_id=$1 AND employee_id=$2 AND kind='shop-purchase'`, domain.CompanyID(u.CompanyID), u.ID).Scan(&w.Spent)
	if err != nil {
		return w, err
	}
	err = p.pool.QueryRow(ctx, `SELECT COALESCE(SUM((payload->'data'->>'delta')::bigint),0) FROM operations WHERE company_id=$1 AND employee_id=$2 AND kind='points-adjustment'`, domain.CompanyID(u.CompanyID), u.ID).Scan(&w.Adjusted)
	if err != nil {
		return w, err
	}
	w.Balance = w.Earned + w.Adjusted - w.Spent
	rows, err := p.pool.Query(ctx, `SELECT DISTINCT concat(payload->'data'->>'kind',':',payload->'data'->>'effect') FROM operations WHERE company_id=$1 AND employee_id=$2 AND kind='shop-purchase' AND payload->'data'->>'kind' IN ('border','name-color','name-style')`, domain.CompanyID(u.CompanyID), u.ID)
	if err != nil {
		return w, err
	}
	defer rows.Close()
	for rows.Next() {
		var effect string
		if err = rows.Scan(&effect); err != nil {
			return w, err
		}
		w.OwnedDecorations = append(w.OwnedDecorations, effect)
	}
	return w, rows.Err()
}
func (m *Memory) ShopWallet(ctx context.Context, u domain.Employee) (domain.ShopWallet, error) {
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	return domain.CommunityWallet(&s, u.ID), err
}
func (p *Postgres) ShopPurchases(ctx context.Context, u domain.Employee, queue bool, cursor string, limit int) (PurchasePage, error) {
	out := PurchasePage{Items: []ShopPurchase{}}
	rows, err := p.pool.Query(ctx, `SELECT o.payload,COALESCE(e.payload->>'name','Former employee') FROM operations o LEFT JOIN employees e ON e.id=o.employee_id AND e.company_id=o.company_id WHERE o.company_id=$1 AND o.kind='shop-purchase' AND o.id>$2 AND (($3 AND o.payload->>'status'='pending') OR (NOT $3 AND o.employee_id=$4)) ORDER BY o.id LIMIT $5`, domain.CompanyID(u.CompanyID), cursor, queue && u.Role == "owner", u.ID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var item ShopPurchase
		if err = rows.Scan(&b, &item.EmployeeName); err != nil {
			return out, err
		}
		if err = json.Unmarshal(b, &item.Operation); err != nil {
			return out, err
		}
		if len(out.Items) == limit {
			out.NextCursor = out.Items[len(out.Items)-1].ID
			break
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}
func (m *Memory) ShopPurchases(ctx context.Context, u domain.Employee, queue bool, cursor string, limit int) (PurchasePage, error) {
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	out := PurchasePage{Items: []ShopPurchase{}}
	if err != nil {
		return out, err
	}
	queue = queue && u.Role == "owner"
	for _, op := range s.Operations {
		if op.Kind == "shop-purchase" && op.ID > cursor && (queue && op.Status == "pending" || !queue && op.EmployeeID == u.ID) {
			name := "Former employee"
			if e := s.Employee(op.EmployeeID); e != nil {
				name = e.Name
			}
			out.Items = append(out.Items, ShopPurchase{op, name})
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].ID < out.Items[j].ID })
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[len(out.Items)-1].ID
	}
	return out, nil
}

func (p *Postgres) PurchaseByID(ctx context.Context, u domain.Employee, id string) (ShopPurchase, error) {
	var out ShopPurchase
	var b []byte
	err := p.pool.QueryRow(ctx, `SELECT o.payload,COALESCE(e.payload->>'name','Former employee') FROM operations o LEFT JOIN employees e ON e.id=o.employee_id AND e.company_id=o.company_id WHERE o.company_id=$1 AND o.kind='shop-purchase' AND o.id=$2 AND (o.employee_id=$3 OR $4)`, domain.CompanyID(u.CompanyID), id, u.ID, u.Role == "owner").Scan(&b, &out.EmployeeName)
	if err == nil {
		err = json.Unmarshal(b, &out.Operation)
	}
	return out, err
}
func (m *Memory) PurchaseByID(ctx context.Context, u domain.Employee, id string) (ShopPurchase, error) {
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	if err != nil {
		return ShopPurchase{}, err
	}
	for _, op := range s.Operations {
		if op.ID == id && op.Kind == "shop-purchase" && (op.EmployeeID == u.ID || u.Role == "owner") {
			name := "Former employee"
			if e := s.Employee(op.EmployeeID); e != nil {
				name = e.Name
			}
			return ShopPurchase{op, name}, nil
		}
	}
	return ShopPurchase{}, errors.New("purchase not found")
}
