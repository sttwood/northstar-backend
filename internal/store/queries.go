package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"northstar/backend/internal/domain"
	"sort"
	"strings"
	"time"
)

type IdentityLookup interface {
	SessionUser(context.Context, string) (domain.Employee, string, error)
	LoginUsers(context.Context, string, string) ([]domain.Employee, error)
}

func (p *Postgres) Operation(ctx context.Context, id string) (domain.Operation, error) {
	var op domain.Operation
	var b []byte
	err := p.pool.QueryRow(ctx, "SELECT payload FROM operations WHERE id=$1", id).Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &op)
	}
	return op, err
}
func (p *Postgres) Company(ctx context.Context, slug string) (domain.Company, error) {
	var c domain.Company
	var b []byte
	err := p.pool.QueryRow(ctx, "SELECT payload FROM companies WHERE slug=$1", slug).Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	return c, err
}
func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (m *Memory) Ping(ctx context.Context) error   { return ctx.Err() }
func (p *Postgres) Companies(ctx context.Context) ([]domain.Company, error) {
	rows, err := p.pool.Query(ctx, "SELECT payload FROM companies ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Company{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var c domain.Company
		if err = json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (m *Memory) Companies(ctx context.Context) ([]domain.Company, error) {
	s, err := m.Read(ctx)
	return s.Companies, err
}
func (p *Postgres) SessionUser(ctx context.Context, hash string) (domain.Employee, string, error) {
	var user domain.Employee
	var b []byte
	var password, company, assurance string
	err := p.pool.QueryRow(ctx, "SELECT e.payload,e.password_hash,e.company_id,s.assurance FROM sessions s JOIN employees e ON e.id=s.employee_id WHERE s.hash=$1 AND s.expires_at>now()", hash).Scan(&b, &password, &company, &assurance)
	if err != nil {
		return user, "", err
	}
	err = json.Unmarshal(b, &user)
	user.PasswordHash = password
	user.CompanyID = company
	return user, assurance, err
}
func (p *Postgres) LoginUsers(ctx context.Context, email, slug string) ([]domain.Employee, error) {
	users := []domain.Employee{}
	rows, err := p.pool.Query(ctx, "SELECT e.payload,e.password_hash,e.company_id FROM employees e JOIN companies c ON c.id=e.company_id WHERE e.email=$1 AND ($2='' OR c.slug=$2)", email, slug)
	if err != nil {
		return users, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var hash, company string
		if err = rows.Scan(&b, &hash, &company); err != nil {
			return users, err
		}
		var u domain.Employee
		if err = json.Unmarshal(b, &u); err != nil {
			return users, err
		}
		u.PasswordHash = hash
		u.CompanyID = company
		users = append(users, u)
	}
	return users, rows.Err()
}
func (m *Memory) SessionUser(ctx context.Context, hash string) (domain.Employee, string, error) {
	s, err := m.Read(ctx)
	if err != nil {
		return domain.Employee{}, "", err
	}
	for _, v := range s.Sessions {
		if v.Hash == hash && v.Expires.After(time.Now()) {
			if u := s.Employee(v.EmployeeID); u != nil {
				return *u, v.Assurance, nil
			}
		}
	}
	return domain.Employee{}, "", errors.New("session missing")
}
func (m *Memory) LoginUsers(ctx context.Context, email, slug string) ([]domain.Employee, error) {
	s, err := m.Read(ctx)
	out := []domain.Employee{}
	for _, u := range s.Employees {
		c := s.Company(u.CompanyID)
		if u.Email == email && (slug == "" || c != nil && c.Slug == slug) {
			out = append(out, u)
		}
	}
	return out, err
}

type PageFilter struct {
	Kind, Status, EmployeeID, ProjectID, RecordID, Environment, Search, Cursor string
	Limit                                                                      int
	Approvals                                                                  bool
}
type Page struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor string            `json:"nextCursor"`
	Limit      int               `json:"limit"`
}
type Pager interface {
	Page(context.Context, domain.Employee, string, PageFilter) (Page, error)
}
type Statistics struct {
	Used         float64 `json:"used"`
	Sick         float64 `json:"sick"`
	Overtime     float64 `json:"overtime"`
	PendingOT    float64 `json:"pendingOT"`
	PendingLeave float64 `json:"pendingLeave"`
	Open         int     `json:"open"`
}

func (p *Postgres) Statistics(ctx context.Context, u domain.Employee, year, month int) (Statistics, error) {
	var out Statistics
	annual := fmt.Sprintf("%04d-%%", year)
	period := fmt.Sprintf("%04d-%02d-%%", year, month)
	err := p.pool.QueryRow(ctx, `SELECT
 COALESCE(SUM(CASE WHEN kind='leave' AND payload->>'status'='approved' AND payload->'data'->>'type'='vacation' AND payload->'data'->>'startDate' LIKE $3 THEN (payload->'data'->>'days')::numeric ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN kind='leave' AND payload->>'status'='approved' AND payload->'data'->>'type'='sick' AND payload->'data'->>'startDate' LIKE $3 THEN (payload->'data'->>'days')::numeric ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN kind='overtime' AND payload->>'status'='approved' AND payload->'data'->>'date' LIKE $4 THEN (payload->'data'->>'hours')::numeric ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN kind='overtime' AND payload->>'status'='pending' AND payload->'data'->>'date' LIKE $4 THEN (payload->'data'->>'hours')::numeric ELSE 0 END),0),
 COALESCE(SUM(CASE WHEN kind='leave' AND payload->>'status'='pending' AND payload->'data'->>'startDate' LIKE $3 THEN (payload->'data'->>'days')::numeric ELSE 0 END),0),
 COUNT(*) FILTER(WHERE kind IN ('document','request','ticket','leave','overtime') AND payload->>'status' IN ('pending','open','in-progress'))
 FROM records WHERE company_id=$1 AND employee_id=$2`, domain.CompanyID(u.CompanyID), u.ID, annual, period).Scan(&out.Used, &out.Sick, &out.Overtime, &out.PendingOT, &out.PendingLeave, &out.Open)
	return out, err
}
func (m *Memory) Statistics(ctx context.Context, u domain.Employee, year, month int) (Statistics, error) {
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	out := Statistics{}
	annual := fmt.Sprintf("%04d-", year)
	period := fmt.Sprintf("%04d-%02d-", year, month)
	for _, r := range s.Records {
		if r.EmployeeID != u.ID {
			continue
		}
		if r.Kind == "leave" && strings.HasPrefix(domain.Text(r, "startDate"), annual) {
			if r.Status == "approved" {
				if domain.Text(r, "type") == "vacation" {
					out.Used += domain.Number(r, "days")
				}
				if domain.Text(r, "type") == "sick" {
					out.Sick += domain.Number(r, "days")
				}
			}
			if r.Status == "pending" {
				out.PendingLeave += domain.Number(r, "days")
			}
		}
		if r.Kind == "overtime" && strings.HasPrefix(domain.Text(r, "date"), period) {
			if r.Status == "approved" {
				out.Overtime += domain.Number(r, "hours")
			}
			if r.Status == "pending" {
				out.PendingOT += domain.Number(r, "hours")
			}
		}
		if domain.Contains([]string{"document", "request", "ticket", "leave", "overtime"}, r.Kind) && domain.Contains([]string{"pending", "open", "in-progress"}, r.Status) {
			out.Open++
		}
	}
	return out, err
}

func RedactEmployee(e domain.Employee, u domain.Employee) domain.Employee {
	if !domain.Manager(u) && e.ID != u.ID {
		e.Salary = 0
		e.Phone = ""
		e.Location = ""
		e.StartDate = ""
		e.RetainUntil = ""
	}
	return e
}
func RedactRecord(v domain.Record, u domain.Employee) domain.Record {
	if v.Kind == "leave" && v.EmployeeID != u.ID && !domain.Manager(u) {
		v.Title = "Team leave"
		v.Data = map[string]any{"type": v.Data["type"], "startDate": v.Data["startDate"], "endDate": v.Data["endDate"], "days": v.Data["days"], "unit": v.Data["unit"], "startTime": v.Data["startTime"], "endTime": v.Data["endTime"]}
	}
	return v
}
func (p *Postgres) Page(ctx context.Context, u domain.Employee, resource string, f PageFilter) (Page, error) {
	out := Page{Items: []json.RawMessage{}, Limit: f.Limit}
	table := ""
	switch resource {
	case "records", "employees", "files":
		table = resource
	case "audit":
		if !domain.Manager(u) {
			return out, errors.New("manager permission required")
		}
		table = "audit_log"
	default:
		return out, errors.New("invalid resource")
	}
	args := []any{domain.CompanyID(u.CompanyID)}
	predicates := []string{"x.company_id=$1"}
	add := func(sql string, value any) {
		args = append(args, value)
		predicates = append(predicates, fmt.Sprintf(sql, len(args)))
	}
	add("x.id > $%d", f.Cursor)
	if resource == "records" {
		if f.RecordID != "" {
			add("x.id=$%d", f.RecordID)
		}
		predicates = append(predicates, "x.kind NOT IN ('community-post','community-answer','shop-item')")
		if f.Approvals {
			if u.Role != "owner" {
				predicates = append(predicates, "FALSE")
			} else {
				add("x.kind IN ('leave','overtime','document','request') AND x.payload->>'status'='pending' AND x.employee_id <> $%d", u.ID)
			}
		}
		if f.Kind != "" {
			add("x.kind=$%d", f.Kind)
		}
		if f.Status != "" {
			add("x.payload->>'status'=$%d", f.Status)
		}
		if f.EmployeeID != "" {
			add("x.employee_id=$%d", f.EmployeeID)
		}
		if f.ProjectID != "" {
			add("x.payload->'data'->>'projectId'=$%d", f.ProjectID)
		}
		if !domain.Manager(u) {
			predicates = append(predicates, "x.kind <> 'template'")
			args = append(args, u.ID)
			n := len(args)
			predicates = append(predicates, fmt.Sprintf(`(x.employee_id=$%[1]d OR x.kind IN ('holiday','benefit') OR (x.kind='leave' AND x.payload->>'status'='approved') OR (x.kind='project' AND x.payload->'data'->'members' ? $%[1]d) OR (x.kind IN ('task','comment') AND EXISTS (SELECT 1 FROM records p WHERE p.company_id=x.company_id AND p.kind='project' AND p.id=x.payload->'data'->>'projectId' AND (p.employee_id=$%[1]d OR p.payload->'data'->'members' ? $%[1]d))))`, n))
		}
	}
	if resource == "files" {
		predicates = append(predicates, "x.payload->>'category'<>'community-image'")
		if !domain.Manager(u) {
			add(`((x.payload->>'category'='project-resource' AND EXISTS (SELECT 1 FROM records p WHERE p.company_id=x.company_id AND p.kind='project' AND p.id=x.payload->>'projectId' AND (p.employee_id=$%[1]d OR p.payload->'data'->'members' ? $%[1]d))) OR (x.payload->>'category'='avatar' AND EXISTS (SELECT 1 FROM employees e WHERE e.id=x.employee_id AND e.company_id=x.company_id AND e.payload->>'avatarFileId'=x.id)) OR (x.payload->>'category' NOT IN ('avatar','project-resource','payslip-reference') AND x.employee_id=$%[1]d))`, u.ID)
		} else if u.Role != "owner" {
			predicates = append(predicates, "x.payload->>'category'<>'payslip-reference'")
		}
		if f.EmployeeID != "" {
			add("x.employee_id=$%d", f.EmployeeID)
		}
		if f.Kind != "" {
			add("x.payload->>'category'=$%d", f.Kind)
		}
		if f.ProjectID != "" {
			add("x.payload->>'projectId'=$%d", f.ProjectID)
		}
		if f.RecordID != "" {
			add("x.payload->>'recordId'=$%d", f.RecordID)
		}
		if f.Environment != "" {
			add("x.payload->>'environment'=$%d", f.Environment)
		}
	}
	if f.Search != "" {
		expression := "x.payload->>'" + map[string]string{"employees": "name", "records": "title", "files": "name", "audit": "action"}[resource] + "'"
		if resource == "records" && !domain.Manager(u) {
			args = append(args, u.ID)
			expression = fmt.Sprintf("CASE WHEN x.kind='leave' AND x.employee_id<>$%d THEN 'Team leave' ELSE x.payload->>'title' END", len(args))
		}
		add("position(lower($%d) in lower("+expression+"))>0", f.Search)
	}
	args = append(args, f.Limit+1)
	rows, err := p.pool.Query(ctx, "SELECT x.id,x.payload FROM "+table+" x WHERE "+strings.Join(predicates, " AND ")+fmt.Sprintf(" ORDER BY x.id LIMIT $%d", len(args)), args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	last := ""
	for rows.Next() {
		var id string
		var b []byte
		if err = rows.Scan(&id, &b); err != nil {
			return out, err
		}
		if len(out.Items) == f.Limit {
			out.NextCursor = last
			break
		}
		last = id
		if resource == "employees" {
			var e domain.Employee
			if err = json.Unmarshal(b, &e); err != nil {
				return out, err
			}
			e.CompanyID = domain.CompanyID(u.CompanyID)
			b, _ = json.Marshal(RedactEmployee(e, u))
		}
		if resource == "records" {
			var v domain.Record
			if err = json.Unmarshal(b, &v); err != nil {
				return out, err
			}
			b, _ = json.Marshal(RedactRecord(v, u))
		}
		out.Items = append(out.Items, json.RawMessage(b))
	}
	return out, rows.Err()
}
func (m *Memory) Page(ctx context.Context, u domain.Employee, resource string, f PageFilter) (Page, error) {
	s, err := m.Read(domain.WithTenant(ctx, u.CompanyID))
	out := Page{Items: []json.RawMessage{}, Limit: f.Limit}
	if err != nil {
		return out, err
	}
	type item struct {
		id    string
		value any
	}
	items := []item{}
	switch resource {
	case "records":
		for _, v := range s.Records {
			v = RedactRecord(v, u)
			if f.RecordID != "" && v.ID != f.RecordID {
				continue
			}
			if f.Approvals && !domain.CanApprove(&s, u, v) {
				continue
			}
			if domain.CanRead(&s, u, v) && (f.Kind == "" || v.Kind == f.Kind) && (f.Status == "" || v.Status == f.Status) && (f.EmployeeID == "" || v.EmployeeID == f.EmployeeID) && (f.ProjectID == "" || domain.Text(v, "projectId") == f.ProjectID) && (f.Search == "" || strings.Contains(strings.ToLower(v.Title), strings.ToLower(f.Search))) {
				items = append(items, item{v.ID, RedactRecord(v, u)})
			}
		}
	case "employees":
		for _, v := range s.Employees {
			if f.Search == "" || strings.Contains(strings.ToLower(v.Name), strings.ToLower(f.Search)) {
				items = append(items, item{v.ID, RedactEmployee(v, u)})
			}
		}
	case "files":
		for _, v := range s.Files {
			if v.Category == "community-image" {
				continue
			}
			if domain.CanReadFile(&s, u, v) && (f.Environment == "" || v.Environment == f.Environment) && (f.EmployeeID == "" || v.EmployeeID == f.EmployeeID) && (f.Kind == "" || v.Category == f.Kind) && (f.ProjectID == "" || v.ProjectID == f.ProjectID) && (f.RecordID == "" || v.RecordID == f.RecordID) && (f.Search == "" || strings.Contains(strings.ToLower(v.Name), strings.ToLower(f.Search))) {
				items = append(items, item{v.ID, v})
			}
		}
	case "audit":
		if !domain.Manager(u) {
			return out, errors.New("manager permission required")
		}
		for _, v := range s.Audit {
			items = append(items, item{v.ID, v})
		}
	default:
		return out, errors.New("invalid resource")
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	last := ""
	for _, v := range items {
		if v.id <= f.Cursor {
			continue
		}
		if len(out.Items) == f.Limit {
			out.NextCursor = last
			break
		}
		last = v.id
		b, _ := json.Marshal(v.value)
		out.Items = append(out.Items, b)
	}
	return out, nil
}
