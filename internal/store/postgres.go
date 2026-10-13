package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"northstar/backend/internal/domain"
	"reflect"
)

//go:embed schema.sql
var schema string

//go:embed migrations/002_production.sql
var productionSchema string

//go:embed migrations/003_projects_resources.sql
var projectsSchema string

//go:embed migrations/004_community.sql
var communitySchema string

//go:embed migrations/005_shop_notifications.sql
var shopSchema string

//go:embed migrations/006_owner_controls.sql
var ownerControlsSchema string

type Postgres struct{ pool *pgxpool.Pool }
type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func NewPostgres(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		pool.Close()
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(7141025)"); err == nil {
		_, err = tx.Exec(ctx, schema)
	}
	var applied bool
	if err == nil {
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=2)").Scan(&applied)
	}
	if err == nil && !applied {
		_, err = tx.Exec(ctx, productionSchema)
	}
	if err == nil {
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=3)").Scan(&applied)
	}
	if err == nil && !applied {
		_, err = tx.Exec(ctx, projectsSchema)
	}
	if err == nil {
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=4)").Scan(&applied)
	}
	if err == nil && !applied {
		_, err = tx.Exec(ctx, communitySchema)
	}
	if err == nil {
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=5)").Scan(&applied)
	}
	if err == nil && !applied {
		_, err = tx.Exec(ctx, shopSchema)
	}
	if err == nil {
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=6)").Scan(&applied)
	}
	if err == nil && !applied {
		_, err = tx.Exec(ctx, ownerControlsSchema)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}
func (p *Postgres) Close() { p.pool.Close() }
func load(ctx context.Context, q queryer) (domain.State, error) {
	s := domain.State{Employees: []domain.Employee{}, Records: []domain.Record{}, Files: []domain.File{}, Audit: []domain.Audit{}, Sessions: []domain.Session{}}
	tenant := domain.Tenant(ctx)
	where := ""
	args := []any{}
	if tenant != "" {
		where = " WHERE company_id=$1"
		args = append(args, tenant)
	}
	rows, err := q.Query(ctx, "SELECT payload, password_hash, company_id FROM employees"+where+" ORDER BY id", args...)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var b []byte
		var hash string
		var company string
		if err = rows.Scan(&b, &hash, &company); err != nil {
			rows.Close()
			return s, err
		}
		var u domain.Employee
		if err = json.Unmarshal(b, &u); err != nil {
			rows.Close()
			return s, err
		}
		u.PasswordHash = hash
		u.CompanyID = company
		s.Employees = append(s.Employees, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	for _, table := range []string{"records", "files", "audit_log", "operations", "account_security", "companies"} {
		filter := where
		order := "id"
		if table == "companies" && tenant != "" {
			filter = " WHERE id=$1"
		}
		if table == "account_security" {
			order = "employee_id"
		}
		rows, err = q.Query(ctx, "SELECT payload FROM "+table+filter+" ORDER BY "+order, args...)
		if err != nil {
			return s, err
		}
		for rows.Next() {
			var b []byte
			if err = rows.Scan(&b); err != nil {
				rows.Close()
				return s, err
			}
			switch table {
			case "records":
				var v domain.Record
				err = json.Unmarshal(b, &v)
				v.CompanyID = domain.CompanyID(v.CompanyID)
				s.Records = append(s.Records, v)
			case "files":
				var v domain.File
				err = json.Unmarshal(b, &v)
				v.CompanyID = domain.CompanyID(v.CompanyID)
				s.Files = append(s.Files, v)
			case "companies":
				var v domain.Company
				err = json.Unmarshal(b, &v)
				s.Companies = append(s.Companies, v)
			case "operations":
				var v domain.Operation
				err = json.Unmarshal(b, &v)
				s.Operations = append(s.Operations, v)
			case "account_security":
				var v domain.AccountSecurity
				err = json.Unmarshal(b, &v)
				s.Security = append(s.Security, v)
			default:
				var v domain.Audit
				err = json.Unmarshal(b, &v)
				v.CompanyID = domain.CompanyID(v.CompanyID)
				s.Audit = append(s.Audit, v)
			}
			if err != nil {
				rows.Close()
				return s, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return s, err
		}
	}
	sessionSQL := "SELECT hash, employee_id, expires_at, assurance FROM sessions WHERE expires_at > now()"
	if tenant != "" {
		sessionSQL += " AND employee_id IN (SELECT id FROM employees WHERE company_id=$1)"
	}
	rows, err = q.Query(ctx, sessionSQL, args...)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var v domain.Session
		if err = rows.Scan(&v.Hash, &v.EmployeeID, &v.Expires, &v.Assurance); err != nil {
			rows.Close()
			return s, err
		}
		s.Sessions = append(s.Sessions, v)
	}
	err = rows.Err()
	rows.Close()
	return s, err
}
func (p *Postgres) Read(ctx context.Context) (domain.State, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return domain.State{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	s, err := load(ctx, tx)
	if err != nil {
		return s, err
	}
	return s, tx.Commit(ctx)
}
func (p *Postgres) Update(ctx context.Context, fn func(*domain.State) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize cross-record invariants (leave balances, approvals, project membership).
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,7141026))", domain.Tenant(ctx)); err != nil {
		return err
	}
	before, err := load(ctx, tx)
	if err != nil {
		return err
	}
	after := clone(before)
	if err = fn(&after); err != nil {
		return err
	}
	if err = persist(ctx, tx, before, after); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func persist(ctx context.Context, tx pgx.Tx, before, after domain.State) error {
	for _, c := range after.Companies {
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO companies(id,slug,payload) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET slug=$2,payload=$3", c.ID, c.Slug, b); err != nil {
			return err
		}
	}
	for _, u := range after.Employees {
		old := before.Employee(u.ID)
		if old != nil && reflect.DeepEqual(*old, u) {
			continue
		}
		b, err := json.Marshal(u)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO employees(id,email,password_hash,payload,company_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET email=$2,password_hash=$3,payload=$4", u.ID, u.Email, u.PasswordHash, b, domain.CompanyID(u.CompanyID)); err != nil {
			return err
		}
	}
	type item struct {
		id, kind, employee, company, status string
		value                               any
	}
	groups := []struct {
		table     string
		old, next []item
	}{{table: "records"}, {table: "files"}, {table: "audit_log"}, {table: "operations"}, {table: "account_security"}}
	for _, r := range before.Records {
		groups[0].old = append(groups[0].old, item{id: r.ID, value: r})
	}
	for _, r := range after.Records {
		groups[0].next = append(groups[0].next, item{id: r.ID, kind: r.Kind, employee: r.EmployeeID, company: domain.CompanyID(r.CompanyID), value: r})
	}
	for _, r := range before.Files {
		groups[1].old = append(groups[1].old, item{id: r.ID, value: r})
	}
	for _, r := range after.Files {
		groups[1].next = append(groups[1].next, item{id: r.ID, employee: r.EmployeeID, company: domain.CompanyID(r.CompanyID), value: r})
	}
	for _, r := range before.Audit {
		groups[2].old = append(groups[2].old, item{id: r.ID, value: r})
	}
	for _, r := range after.Audit {
		groups[2].next = append(groups[2].next, item{id: r.ID, company: domain.CompanyID(r.CompanyID), value: r})
	}
	for _, v := range before.Operations {
		groups[3].old = append(groups[3].old, item{id: v.ID, value: v})
	}
	for _, v := range after.Operations {
		groups[3].next = append(groups[3].next, item{id: v.ID, company: domain.CompanyID(v.CompanyID), employee: v.EmployeeID, kind: v.Kind, status: v.Status, value: v})
	}
	for _, v := range before.Security {
		groups[4].old = append(groups[4].old, item{id: v.EmployeeID, value: v})
	}
	for _, v := range after.Security {
		groups[4].next = append(groups[4].next, item{id: v.EmployeeID, company: domain.CompanyID(v.CompanyID), value: v})
	}
	for _, g := range groups {
		old := map[string]any{}
		next := map[string]bool{}
		for _, i := range g.old {
			old[i.id] = i.value
		}
		for _, i := range g.next {
			next[i.id] = true
			if reflect.DeepEqual(old[i.id], i.value) {
				continue
			}
			b, err := json.Marshal(i.value)
			if err != nil {
				return err
			}
			var sql string
			var args []any
			switch g.table {
			case "records":
				sql = "INSERT INTO records(id,kind,employee_id,payload,company_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET kind=$2,employee_id=$3,payload=$4"
				args = []any{i.id, i.kind, i.employee, b, i.company}
			case "files":
				sql = "INSERT INTO files(id,employee_id,payload,company_id) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET payload=$3"
				args = []any{i.id, i.employee, b, i.company}
			case "operations":
				sql = "INSERT INTO operations(id,company_id,kind,employee_id,status,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET status=$5,payload=$6"
				args = []any{i.id, i.company, i.kind, i.employee, i.status, b}
			case "account_security":
				sql = "INSERT INTO account_security(employee_id,company_id,payload) VALUES($1,$2,$3) ON CONFLICT(employee_id) DO UPDATE SET payload=$3"
				args = []any{i.id, i.company, b}
			default:
				sql = "INSERT INTO audit_log(id,payload,company_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING"
				args = []any{i.id, b, i.company}
			}
			if _, err = tx.Exec(ctx, sql, args...); err != nil {
				return fmt.Errorf("persist %s: %w", g.table, err)
			}
		}
		for id := range old {
			if !next[id] {
				key := "id"
				if g.table == "account_security" {
					key = "employee_id"
				}
				if _, err := tx.Exec(ctx, "DELETE FROM "+g.table+" WHERE "+key+"=$1", id); err != nil {
					return err
				}
			}
		}
	}
	kept := map[string]bool{}
	for _, v := range after.Sessions {
		kept[v.Hash] = true
	}
	for _, v := range before.Sessions {
		if !kept[v.Hash] {
			if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE hash=$1", v.Hash); err != nil {
				return err
			}
		}
	}
	for _, session := range after.Sessions {
		if _, err := tx.Exec(ctx, "INSERT INTO sessions(hash,employee_id,expires_at,assurance) VALUES($1,$2,$3,$4) ON CONFLICT(hash) DO NOTHING", session.Hash, session.EmployeeID, session.Expires, session.Assurance); err != nil {
			return err
		}
	}
	return nil
}
