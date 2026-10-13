package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"northstar/backend/internal/domain"
	"sort"
	"strings"
	"time"
)

type CommunityFilter struct {
	PostID, AnswerID, Type, Status, Search, Cursor string
	Limit                                          int
}

var ErrCommunityPostNotFound = errors.New("community post not found")

type CommunityRepository interface {
	CommunityAuthors(context.Context, domain.Employee, []string) ([]domain.CommunityPerson, error)
	CommunityBadges(context.Context, domain.Employee, []string, time.Time) (map[string][]domain.Achievement, error)
	CommunityPage(context.Context, domain.Employee, CommunityFilter) (domain.CommunityPage, error)
	CommunityPost(context.Context, domain.Employee, string) (domain.Record, error)
	CommunitySummary(context.Context, domain.Employee, time.Time) (domain.CommunitySummary, error)
}

func communityCursor(r domain.Record) string {
	b, _ := json.Marshal([]string{r.CreatedAt, r.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeCommunityCursor(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 1024 {
		return nil, errors.New("invalid community cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	var parts []string
	if err != nil || json.Unmarshal(b, &parts) != nil || len(parts) != 2 || len(parts[1]) > 100 {
		return nil, errors.New("invalid community cursor")
	}
	if _, err = time.Parse(time.RFC3339Nano, parts[0]); err != nil {
		return nil, errors.New("invalid community cursor")
	}
	return parts, nil
}
func (p *Postgres) CommunityPost(ctx context.Context, u domain.Employee, id string) (domain.Record, error) {
	var out domain.Record
	var b []byte
	err := p.pool.QueryRow(ctx, "SELECT payload FROM records WHERE company_id=$1 AND id=$2 AND kind='community-post'", domain.CompanyID(u.CompanyID), id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrCommunityPostNotFound
	}
	if err == nil {
		err = json.Unmarshal(b, &out)
	}
	return out, err
}
func (m *Memory) CommunityPost(ctx context.Context, u domain.Employee, id string) (domain.Record, error) {
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	if err != nil {
		return domain.Record{}, err
	}
	for _, r := range s.Records {
		if r.ID == id && r.Kind == "community-post" {
			return r, nil
		}
	}
	return domain.Record{}, ErrCommunityPostNotFound
}
func (p *Postgres) CommunityPage(ctx context.Context, u domain.Employee, f CommunityFilter) (domain.CommunityPage, error) {
	out := domain.CommunityPage{Items: []domain.Record{}, Authors: []domain.CommunityPerson{}}
	cursor, err := decodeCommunityCursor(f.Cursor)
	if err != nil {
		return out, err
	}
	kind := "community-post"
	if f.PostID != "" {
		kind = "community-answer"
	}
	args := []any{domain.CompanyID(u.CompanyID), kind}
	where := []string{"x.company_id=$1", "x.kind=$2"}
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if f.AnswerID != "" {
		add("x.id=$%d", f.AnswerID)
	}
	if f.PostID != "" {
		add("x.payload->'data'->>'postId'=$%d", f.PostID)
	} else {
		if f.Type != "" {
			add("x.payload->'data'->>'type'=$%d", f.Type)
		}
		if f.Status != "" {
			add("x.payload->>'status'=$%d", f.Status)
		}
		if f.Search != "" {
			add("position(lower($%d) in lower(concat(x.payload->>'title',' ',x.payload->'data'->>'body',' ',x.payload->'data'->>'tags')))>0", f.Search)
		}
	}
	if cursor != nil {
		args = append(args, cursor[0], cursor[1])
		where = append(where, fmt.Sprintf("(x.payload->>'createdAt',x.id)<($%d,$%d)", len(args)-1, len(args)))
	}
	args = append(args, f.Limit+1)
	rows, err := p.pool.Query(ctx, "SELECT x.payload,e.payload FROM records x LEFT JOIN employees e ON e.id=x.employee_id AND e.company_id=x.company_id WHERE "+strings.Join(where, " AND ")+fmt.Sprintf(" ORDER BY x.payload->>'createdAt' DESC,x.id DESC LIMIT $%d", len(args)), args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	authors := map[string]bool{}
	for rows.Next() {
		var b, person []byte
		if err = rows.Scan(&b, &person); err != nil {
			return out, err
		}
		if len(out.Items) == f.Limit {
			out.NextCursor = communityCursor(out.Items[len(out.Items)-1])
			break
		}
		var r domain.Record
		var employee domain.Employee
		if err = json.Unmarshal(b, &r); err != nil {
			return out, err
		}
		_ = json.Unmarshal(person, &employee)
		if employee.ID == "" {
			employee.ID = r.EmployeeID
			employee.Name = "Former employee"
		}
		out.Items = append(out.Items, r)
		if !authors[employee.ID] {
			out.Authors = append(out.Authors, domain.CommunityAuthor(employee))
			authors[employee.ID] = true
		}
	}
	return out, rows.Err()
}
func (m *Memory) CommunityPage(ctx context.Context, u domain.Employee, f CommunityFilter) (domain.CommunityPage, error) {
	out := domain.CommunityPage{Items: []domain.Record{}, Authors: []domain.CommunityPerson{}}
	cursor, err := decodeCommunityCursor(f.Cursor)
	if err != nil {
		return out, err
	}
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	if err != nil {
		return out, err
	}
	items := []domain.Record{}
	for _, r := range s.Records {
		if f.AnswerID != "" && r.ID != f.AnswerID {
			continue
		}
		if f.PostID != "" {
			if r.Kind != "community-answer" || domain.Text(r, "postId") != f.PostID {
				continue
			}
		} else {
			if r.Kind != "community-post" || f.Type != "" && domain.Text(r, "type") != f.Type || f.Status != "" && r.Status != f.Status {
				continue
			}
			if f.Search != "" && !strings.Contains(strings.ToLower(r.Title+" "+domain.Text(r, "body")+" "+strings.Join(domain.Strings(r, "tags"), " ")), strings.ToLower(f.Search)) {
				continue
			}
		}
		if cursor != nil && (r.CreatedAt > cursor[0] || r.CreatedAt == cursor[0] && r.ID >= cursor[1]) {
			continue
		}
		items = append(items, r)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt == items[j].CreatedAt {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt > items[j].CreatedAt
	})
	authors := map[string]bool{}
	for _, r := range items {
		if len(out.Items) == f.Limit {
			out.NextCursor = communityCursor(out.Items[len(out.Items)-1])
			break
		}
		out.Items = append(out.Items, r)
		if !authors[r.EmployeeID] {
			person := domain.CommunityPerson{ID: r.EmployeeID, Name: "Former employee"}
			if e := s.Employee(r.EmployeeID); e != nil {
				person = domain.CommunityAuthor(*e)
			}
			out.Authors = append(out.Authors, person)
			authors[r.EmployeeID] = true
		}
	}
	return out, nil
}

const acceptedCommunityJoin = ` FROM records q JOIN records a ON a.company_id=q.company_id AND a.id=q.payload->'data'->>'acceptedAnswerId' AND a.kind='community-answer' AND a.payload->'data'->>'postId'=q.id WHERE q.company_id=$1 AND q.kind='community-post' AND q.payload->'data'->>'type'='help' AND q.payload->>'status'='resolved' AND a.employee_id<>q.employee_id AND a.employee_id<>COALESCE(q.payload->'data'->>'acceptedBy','')`

func (p *Postgres) CommunitySummary(ctx context.Context, u domain.Employee, now time.Time) (domain.CommunitySummary, error) {
	out := domain.CommunitySummary{Leaders: []domain.CommunityLeader{}}
	posts, answers, knowledge, resolved := 0, 0, 0, 0
	company := domain.CompanyID(u.CompanyID)
	err := p.pool.QueryRow(ctx, `SELECT COUNT(*) FILTER(WHERE kind='community-post'),COUNT(*) FILTER(WHERE kind='community-answer'),COUNT(*) FILTER(WHERE kind='community-post' AND payload->'data'->>'type'='knowledge') FROM records WHERE company_id=$1 AND employee_id=$2 AND kind IN ('community-post','community-answer')`, company, u.ID).Scan(&posts, &answers, &knowledge)
	if err != nil {
		return out, err
	}
	if err = p.pool.QueryRow(ctx, "SELECT COUNT(*)"+acceptedCommunityJoin+" AND a.employee_id=$2", company, u.ID).Scan(&resolved); err != nil {
		return out, err
	}
	rows, err := p.pool.Query(ctx, "SELECT payload->'data'->>'date' FROM operations WHERE company_id=$1 AND employee_id=$2 AND kind='community-activity'", company, u.ID)
	if err != nil {
		return out, err
	}
	dates := []string{}
	for rows.Next() {
		var day string
		if err = rows.Scan(&day); err != nil {
			rows.Close()
			return out, err
		}
		dates = append(dates, day)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Stats = domain.BuildCommunityStats(posts, answers, knowledge, resolved, dates, now)
	rows, err = p.pool.Query(ctx, `WITH scores AS (SELECT a.employee_id,COUNT(*) AS resolved`+acceptedCommunityJoin+` GROUP BY a.employee_id) SELECT e.payload,s.resolved FROM scores s JOIN employees e ON e.id=s.employee_id WHERE e.company_id=$1 AND COALESCE(e.payload->>'status','active') IN ('active','') ORDER BY s.resolved DESC,lower(e.payload->>'name'),e.id LIMIT 10`, company)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var e domain.Employee
		var count int
		if err = rows.Scan(&b, &count); err != nil {
			return out, err
		}
		if err = json.Unmarshal(b, &e); err != nil {
			return out, err
		}
		out.Leaders = append(out.Leaders, domain.CommunityLeader{Person: domain.CommunityAuthor(e), Resolved: count, Points: count * 10})
	}
	return out, rows.Err()
}
func (m *Memory) CommunitySummary(ctx context.Context, u domain.Employee, now time.Time) (domain.CommunitySummary, error) {
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	out := domain.CommunitySummary{Leaders: []domain.CommunityLeader{}}
	if err != nil {
		return out, err
	}
	posts, answers, knowledge := 0, 0, 0
	byID := map[string]domain.Record{}
	scores := map[string]int{}
	for _, r := range s.Records {
		byID[r.ID] = r
		if r.EmployeeID == u.ID {
			if r.Kind == "community-post" {
				posts++
				if domain.Text(r, "type") == "knowledge" {
					knowledge++
				}
			} else if r.Kind == "community-answer" {
				answers++
			}
		}
	}
	for _, r := range s.Records {
		if r.Kind == "community-post" && r.Status == "resolved" && domain.Text(r, "type") == "help" {
			a, ok := byID[domain.Text(r, "acceptedAnswerId")]
			if ok && a.Kind == "community-answer" && domain.Text(a, "postId") == r.ID && a.EmployeeID != r.EmployeeID && a.EmployeeID != domain.Text(r, "acceptedBy") {
				scores[a.EmployeeID]++
			}
		}
	}
	dates := []string{}
	for _, op := range s.Operations {
		if op.Kind == "community-activity" && op.EmployeeID == u.ID {
			date, _ := op.Data["date"].(string)
			dates = append(dates, date)
		}
	}
	out.Stats = domain.BuildCommunityStats(posts, answers, knowledge, scores[u.ID], dates, now)
	for _, e := range s.Employees {
		if domain.Active(e) && scores[e.ID] > 0 {
			out.Leaders = append(out.Leaders, domain.CommunityLeader{Person: domain.CommunityAuthor(e), Resolved: scores[e.ID], Points: scores[e.ID] * 10})
		}
	}
	sort.Slice(out.Leaders, func(i, j int) bool {
		a, b := out.Leaders[i], out.Leaders[j]
		if a.Points != b.Points {
			return a.Points > b.Points
		}
		if strings.ToLower(a.Person.Name) != strings.ToLower(b.Person.Name) {
			return strings.ToLower(a.Person.Name) < strings.ToLower(b.Person.Name)
		}
		return a.Person.ID < b.Person.ID
	})
	if len(out.Leaders) > 10 {
		out.Leaders = out.Leaders[:10]
	}
	return out, nil
}
