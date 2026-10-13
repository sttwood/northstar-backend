package store

import (
	"context"
	"encoding/json"
	"errors"
	"northstar/backend/internal/domain"
	"sort"
)

type CompanyLookup interface {
	CompanyByID(context.Context, string) (domain.Company, error)
}

func (p *Postgres) CompanyByID(ctx context.Context, id string) (domain.Company, error) {
	var c domain.Company
	var b []byte
	err := p.pool.QueryRow(ctx, "SELECT payload FROM companies WHERE id=$1", domain.CompanyID(id)).Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	return c, err
}
func (m *Memory) CompanyByID(ctx context.Context, id string) (domain.Company, error) {
	s, err := m.Read(domain.WithTenant(ctx, id))
	if err != nil {
		return domain.Company{}, err
	}
	c := s.Company(id)
	if c == nil {
		return domain.Company{}, errors.New("company not found")
	}
	return *c, nil
}
func (p *Postgres) PointAdjustments(ctx context.Context, u domain.Employee, target, cursor string, limit int) (PurchasePage, error) {
	out := PurchasePage{Items: []ShopPurchase{}}
	rows, err := p.pool.Query(ctx, `SELECT payload FROM operations WHERE company_id=$1 AND kind='points-adjustment' AND employee_id=$2 AND id>$3 ORDER BY id LIMIT $4`, domain.CompanyID(u.CompanyID), target, cursor, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var op domain.Operation
		if err = rows.Scan(&b); err != nil {
			return out, err
		}
		if err = json.Unmarshal(b, &op); err != nil {
			return out, err
		}
		if len(out.Items) == limit {
			out.NextCursor = out.Items[len(out.Items)-1].ID
			break
		}
		out.Items = append(out.Items, ShopPurchase{Operation: op})
	}
	return out, rows.Err()
}
func (m *Memory) PointAdjustments(ctx context.Context, u domain.Employee, target, cursor string, limit int) (PurchasePage, error) {
	out := PurchasePage{Items: []ShopPurchase{}}
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	if err != nil {
		return out, err
	}
	for _, op := range s.Operations {
		if op.Kind == "points-adjustment" && op.EmployeeID == target && op.ID > cursor {
			out.Items = append(out.Items, ShopPurchase{Operation: op})
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].ID < out.Items[j].ID })
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[len(out.Items)-1].ID
	}
	return out, nil
}
