package store

import (
	"context"
	"encoding/json"
	"northstar/backend/internal/domain"
	"time"
)

func (p *Postgres) CommunityAuthors(ctx context.Context, u domain.Employee, ids []string) ([]domain.CommunityPerson, error) {
	out := []domain.CommunityPerson{}
	rows, err := p.pool.Query(ctx, "SELECT payload FROM employees WHERE company_id=$1 AND id=ANY($2::text[])", domain.CompanyID(u.CompanyID), ids)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var e domain.Employee
		if err = rows.Scan(&b); err != nil {
			return out, err
		}
		if err = json.Unmarshal(b, &e); err != nil {
			return out, err
		}
		out = append(out, domain.CommunityAuthor(e))
	}
	return out, rows.Err()
}
func (m *Memory) CommunityAuthors(ctx context.Context, u domain.Employee, ids []string) ([]domain.CommunityPerson, error) {
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	out := []domain.CommunityPerson{}
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		if e := s.Employee(id); e != nil {
			out = append(out, domain.CommunityAuthor(*e))
		}
	}
	return out, nil
}

func earnedCommunityBadges(stats domain.CommunityStats) []domain.Achievement {
	badges := []domain.Achievement{}
	for _, badge := range stats.Achievements {
		if badge.Unlocked {
			badges = append(badges, badge)
		}
	}
	return badges
}

// At most three aggregate queries per author batch, independent of feed size.
// Unknown and cross-company identities are omitted; no employee/HR data is returned.
func (p *Postgres) CommunityBadges(ctx context.Context, u domain.Employee, ids []string, now time.Time) (map[string][]domain.Achievement, error) {
	type counts struct {
		posts, answers, knowledge, resolved int
		dates                               []string
	}
	values := map[string]*counts{}
	out := map[string][]domain.Achievement{}
	company := domain.CompanyID(u.CompanyID)
	rows, err := p.pool.Query(ctx, `SELECT e.id,COUNT(r.id) FILTER(WHERE r.kind='community-post'),COUNT(r.id) FILTER(WHERE r.kind='community-answer'),COUNT(r.id) FILTER(WHERE r.kind='community-post' AND r.payload->'data'->>'type'='knowledge') FROM employees e LEFT JOIN records r ON r.company_id=e.company_id AND r.employee_id=e.id AND r.kind IN ('community-post','community-answer') WHERE e.company_id=$1 AND e.id=ANY($2::text[]) GROUP BY e.id`, company, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		c := &counts{}
		if err = rows.Scan(&id, &c.posts, &c.answers, &c.knowledge); err != nil {
			rows.Close()
			return nil, err
		}
		values[id] = c
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = p.pool.Query(ctx, "SELECT a.employee_id,COUNT(*)"+acceptedCommunityJoin+" AND a.employee_id=ANY($2::text[]) GROUP BY a.employee_id", company, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var count int
		if err = rows.Scan(&id, &count); err != nil {
			rows.Close()
			return nil, err
		}
		if c := values[id]; c != nil {
			c.resolved = count
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = p.pool.Query(ctx, `SELECT employee_id,payload->'data'->>'date' FROM operations WHERE company_id=$1 AND employee_id=ANY($2::text[]) AND kind='community-activity'`, company, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, day string
		if err = rows.Scan(&id, &day); err != nil {
			rows.Close()
			return nil, err
		}
		if c := values[id]; c != nil {
			c.dates = append(c.dates, day)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for id, c := range values {
		out[id] = earnedCommunityBadges(domain.BuildCommunityStats(c.posts, c.answers, c.knowledge, c.resolved, c.dates, now))
	}
	return out, nil
}

func (m *Memory) CommunityBadges(ctx context.Context, u domain.Employee, ids []string, now time.Time) (map[string][]domain.Achievement, error) {
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	if err != nil {
		return nil, err
	}
	out := map[string][]domain.Achievement{}
	for _, id := range ids {
		if e := s.Employee(id); e != nil {
			summary, err := m.CommunitySummary(ctx, *e, now)
			if err != nil {
				return nil, err
			}
			out[id] = earnedCommunityBadges(summary.Stats)
		}
	}
	return out, nil
}
