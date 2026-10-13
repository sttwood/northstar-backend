package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"northstar/backend/internal/domain"
	"sort"
	"strings"
)

type NotificationPage struct {
	Items       []domain.Operation `json:"items"`
	NextCursor  string             `json:"nextCursor"`
	UnreadCount int                `json:"unreadCount"`
}
type NotificationRepository interface {
	Notifications(context.Context, domain.Employee, string, int) (NotificationPage, error)
}

func notificationCursor(op domain.Operation) string {
	return base64.RawURLEncoding.EncodeToString([]byte(op.CreatedAt + "|" + op.ID))
}
func notificationPosition(cursor string) (string, string, error) {
	if cursor == "" {
		return "", "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	parts := strings.Split(string(b), "|")
	if err != nil || len(parts) != 2 || len(cursor) > 512 {
		return "", "", errors.New("invalid notification cursor")
	}
	return parts[0], parts[1], nil
}
func (p *Postgres) Notifications(ctx context.Context, u domain.Employee, cursor string, limit int) (NotificationPage, error) {
	out := NotificationPage{Items: []domain.Operation{}}
	date, id, err := notificationPosition(cursor)
	if err != nil {
		return out, err
	}
	err = p.pool.QueryRow(ctx, `SELECT COUNT(*) FROM operations WHERE company_id=$1 AND employee_id=$2 AND kind='notification' AND payload->>'status'='unread'`, domain.CompanyID(u.CompanyID), u.ID).Scan(&out.UnreadCount)
	if err != nil {
		return out, err
	}
	rows, err := p.pool.Query(ctx, `SELECT payload FROM operations WHERE company_id=$1 AND employee_id=$2 AND kind='notification' AND ($3='' OR (payload->>'createdAt',id)<($3,$4)) ORDER BY payload->>'createdAt' DESC,id DESC LIMIT $5`, domain.CompanyID(u.CompanyID), u.ID, date, id, limit+1)
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
			out.NextCursor = notificationCursor(out.Items[len(out.Items)-1])
			break
		}
		out.Items = append(out.Items, op)
	}
	return out, rows.Err()
}
func (m *Memory) Notifications(ctx context.Context, u domain.Employee, cursor string, limit int) (NotificationPage, error) {
	out := NotificationPage{Items: []domain.Operation{}}
	date, id, err := notificationPosition(cursor)
	if err != nil {
		return out, err
	}
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	if err != nil {
		return out, err
	}
	for _, op := range s.Operations {
		if op.Kind != "notification" || op.EmployeeID != u.ID {
			continue
		}
		if op.Status == "unread" {
			out.UnreadCount++
		}
		if cursor != "" && (op.CreatedAt > date || op.CreatedAt == date && op.ID >= id) {
			continue
		}
		out.Items = append(out.Items, op)
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.CreatedAt == b.CreatedAt {
			return a.ID > b.ID
		}
		return a.CreatedAt > b.CreatedAt
	})
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = notificationCursor(out.Items[len(out.Items)-1])
	}
	return out, nil
}
