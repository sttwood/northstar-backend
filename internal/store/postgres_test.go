package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/url"
	"northstar/backend/internal/domain"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresPersistenceAndRollback(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set TEST_DATABASE_URL for real PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	schema := fmt.Sprintf("northstar_test_%d", time.Now().UnixNano())
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	}()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	repo, err := NewPostgres(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Update(ctx, func(s *domain.State) error {
		s.Employees = append(s.Employees, domain.Employee{ID: "test", Email: "test@example.com", PasswordHash: "hash", Name: "Test", Role: "employee"})
		s.Records = append(s.Records, domain.Record{ID: "record", Kind: "ticket", EmployeeID: "test", Title: "Persistent ticket", Status: "open", Data: map[string]any{"category": "IT support"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repo.Close()
	repo, err = NewPostgres(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	state, err := repo.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Records) != 1 || state.Employee("test").PasswordHash != "hash" {
		t.Fatal("data did not survive reconnect")
	}
	expected := errors.New("rollback")
	err = repo.Update(ctx, func(s *domain.State) error { s.Records = nil; return expected })
	if !errors.Is(err, expected) {
		t.Fatal("expected transaction error")
	}
	state, err = repo.Read(ctx)
	if err != nil || len(state.Records) != 1 {
		t.Fatal("failed transaction changed persisted records")
	}
	for _, tenant := range []string{"alpha", "beta"} {
		err = repo.Update(domain.WithTenant(ctx, tenant), func(s *domain.State) error {
			s.Companies = append(s.Companies, domain.DefaultPolicy(tenant, tenant, tenant))
			s.Employees = append(s.Employees, domain.Employee{ID: tenant + "-employee", CompanyID: tenant, Email: "same@example.test", Name: tenant, Role: "employee", Status: "active", Salary: 65000, PasswordHash: "private-hash"}, domain.Employee{ID: tenant + "-reviewer", CompanyID: tenant, Email: "reviewer@example.test", Name: "Reviewer", Role: "employee", Status: "active"})
			for n := 0; n < 305; n++ {
				s.Records = append(s.Records, domain.Record{ID: fmt.Sprintf("%s-%04d", tenant, n), CompanyID: tenant, EmployeeID: tenant + "-employee", Kind: "request", Title: "Review this", Status: "pending", Data: map[string]any{"currentApproverIds": []string{tenant + "-reviewer"}, "approvalSteps": []any{map[string]any{"stage": "manager"}}}})
			}
			s.Security = append(s.Security, domain.AccountSecurity{EmployeeID: tenant + "-employee", CompanyID: tenant, TOTPSecret: "encrypted-secret", Enabled: true, LastStep: 42, RecoveryHashes: []string{"hashed-recovery"}})
			s.Operations = append(s.Operations, domain.Operation{ID: tenant + "-operation", CompanyID: tenant, Kind: "email", Status: "queued", Data: map[string]any{"body": "encrypted-body"}})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	alphaCtx := domain.WithTenant(ctx, "alpha")
	alpha, err := repo.Read(alphaCtx)
	if err != nil || len(alpha.Records) != 305 || len(alpha.Security) != 1 || alpha.Security[0].TOTPSecret != "encrypted-secret" || len(alpha.Operations) != 1 {
		t.Fatalf("private tenant persistence failed: %v", err)
	}
	users, err := repo.LoginUsers(ctx, "same@example.test", "alpha")
	if err != nil || len(users) != 1 || users[0].CompanyID != "alpha" {
		t.Fatal("login not tenant-scoped")
	}
	reviewer := *alpha.Employee("alpha-reviewer")
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := repo.Page(alphaCtx, domain.Employee{ID: "alpha-owner", CompanyID: "alpha", Role: "owner"}, "records", PageFilter{Limit: 37, Approvals: true, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 37 {
			t.Fatal("unbounded page")
		}
		for _, raw := range page.Items {
			var record domain.Record
			if json.Unmarshal(raw, &record) != nil || record.CompanyID != "alpha" || seen[record.ID] {
				t.Fatal("pagination leak or duplicate")
			}
			seen[record.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 305 {
		t.Fatalf("pagination dropped rows: %d", len(seen))
	}
	employees, err := repo.Page(alphaCtx, reviewer, "employees", PageFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range employees.Items {
		var employee domain.Employee
		json.Unmarshal(raw, &employee)
		if employee.ID != reviewer.ID && employee.Salary != 0 {
			t.Fatal("salary leak")
		}
	}
	err = repo.Update(alphaCtx, func(s *domain.State) error {
		s.Employees = append(s.Employees, domain.Employee{ID: "alpha-owner", CompanyID: "alpha", Role: "owner", Name: "Owner", Email: "owner@alpha.test"}, domain.Employee{ID: "alpha-hr", CompanyID: "alpha", Role: "hr", Name: "HR", Email: "hr@alpha.test"})
		s.Records = append(s.Records, domain.Record{ID: "alpha-parent", CompanyID: "alpha", Kind: "project", EmployeeID: "alpha-owner", Data: map[string]any{"members": []string{"alpha-employee"}}}, domain.Record{ID: "alpha-child", CompanyID: "alpha", Kind: "project", EmployeeID: "alpha-owner", Data: map[string]any{"parentId": "alpha-parent", "members": []string{"alpha-reviewer"}}})
		for n := 0; n < 71; n++ {
			s.Files = append(s.Files, domain.File{ID: fmt.Sprintf("alpha-resource-%03d", n), CompanyID: "alpha", EmployeeID: "alpha-employee", Category: "project-resource", ProjectID: "alpha-child", ResourceKind: "api-spec", MimeType: "application/json", Description: "Synthetic API", Name: "api.json", Environment: []string{"dev", "prod"}[n%2]})
		}
		s.Files = append(s.Files, domain.File{ID: "alpha-reference", CompanyID: "alpha", EmployeeID: "alpha-employee", Category: "payslip-reference", RecordID: "alpha-template", Name: "reference.pdf"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	alpha, err = repo.Read(alphaCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha.Files) != 72 {
		t.Fatal("file fields did not persist")
	}
	cursor = ""
	resources := map[string]bool{}
	for {
		page, err := repo.Page(alphaCtx, reviewer, "files", PageFilter{Limit: 19, Kind: "project-resource", ProjectID: "alpha-child", Search: "api", Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range page.Items {
			var f domain.File
			_ = json.Unmarshal(raw, &f)
			if resources[f.ID] || f.ProjectID != "alpha-child" || f.MimeType != "application/json" || f.Description != "Synthetic API" {
				t.Fatal("file paging leaked, duplicated or lost metadata")
			}
			resources[f.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(resources) != 71 {
		t.Fatalf("resource paging lost rows: %d", len(resources))
	}
	for env, count := range map[string]int{"dev": 36, "prod": 35, "sit": 0, "uat": 0} {
		page, err := repo.Page(alphaCtx, reviewer, "files", PageFilter{Limit: 100, Kind: "project-resource", ProjectID: "alpha-child", Environment: env})
		if err != nil || len(page.Items) != count {
			t.Fatalf("environment filter %s: got %d, err %v", env, len(page.Items), err)
		}
	}
	for _, id := range []string{"alpha-employee", "alpha-hr", "alpha-owner"} {
		user := *alpha.Employee(id)
		page, err := repo.Page(alphaCtx, user, "files", PageFilter{Limit: 100, Kind: "payslip-reference", RecordID: "alpha-template"})
		if err != nil {
			t.Fatal(err)
		}
		expected := 0
		if user.Role == "owner" {
			expected = 1
		}
		if len(page.Items) != expected {
			t.Fatal("reference permission mismatch")
		}
	}
	page, err := repo.Page(alphaCtx, *alpha.Employee("alpha-employee"), "files", PageFilter{Limit: 100, Kind: "project-resource", ProjectID: "alpha-child", EmployeeID: "alpha-employee"})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("parent membership or uploader bypassed child permission")
	}
	page, err = repo.Page(domain.WithTenant(ctx, "beta"), domain.Employee{ID: "beta-owner", CompanyID: "beta", Role: "owner"}, "files", PageFilter{Limit: 100, ProjectID: "alpha-child"})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("cross-company resource leak")
	}
	err = repo.Update(alphaCtx, func(s *domain.State) error { s.Records = nil; s.Security[0].LastStep = 43; return nil })
	if err != nil {
		t.Fatal(err)
	}
	beta, err := repo.Read(domain.WithTenant(ctx, "beta"))
	if err != nil || len(beta.Records) != 305 || beta.Security[0].LastStep != 42 {
		t.Fatal("tenant update changed another tenant")
	}
}
func TestPostgresCommunityPagingAwardsAndTenantIsolation(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set TEST_DATABASE_URL for real PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	schema := fmt.Sprintf("community_test_%d", time.Now().UnixNano())
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	}()
	parsed, _ := url.Parse(raw)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	repo, err := NewPostgres(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	err = repo.Update(ctx, func(s *domain.State) error {
		s.Employees = append(s.Employees, domain.Employee{ID: "questioner", CompanyID: "default", Role: "employee", Name: "Questioner", Email: "q@test.invalid", AvatarFileID: "profile-file"}, domain.Employee{ID: "helper", CompanyID: "default", Role: "employee", Name: "Helper", Email: "h@test.invalid", Salary: 99999, Phone: "private"})
		for n := 0; n < 47; n++ {
			id := fmt.Sprintf("post-%03d", n)
			kind := "knowledge"
			status := "open"
			values := map[string]any{"type": kind, "body": "Synthetic guide", "tags": []string{"junior"}}
			if n == 46 {
				values["bodyDoc"] = domain.RichNode{Type: "doc", Content: []domain.RichNode{{Type: "paragraph", Content: []domain.RichNode{{Type: "text", Text: "Synthetic guide", Marks: []domain.RichMark{{Type: "bold"}}}}}}}
			}
			if n < 5 {
				values["type"] = "help"
				values["acceptedAnswerId"] = "answer-" + id
				values["acceptedBy"] = "questioner"
				status = "resolved"
				s.Records = append(s.Records, domain.Record{ID: "answer-" + id, CompanyID: "default", EmployeeID: "helper", Kind: "community-answer", Status: "active", CreatedAt: now.UTC().Format("2006-01-02T15:04:05.000000000Z"), Data: map[string]any{"postId": id, "body": "Synthetic answer"}})
			}
			s.Records = append(s.Records, domain.Record{ID: id, CompanyID: "default", EmployeeID: "questioner", Kind: "community-post", Title: "Guide " + id, Status: status, CreatedAt: now.Add(time.Duration(n) * time.Second).UTC().Format("2006-01-02T15:04:05.000000000Z"), Data: values})
		}
		for n := 0; n < 5; n++ {
			domain.AddCommunityActivity(s, domain.Employee{ID: "helper", CompanyID: "default"}, now.AddDate(0, 0, -n), fmt.Sprintf("activity-%d", n))
		}
		s.Files = append(s.Files, domain.File{ID: "profile-file", CompanyID: "default", EmployeeID: "questioner", Category: "avatar", MimeType: "image/png", Name: "photo.png"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	repo.Close()
	repo, err = NewPostgres(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	u := domain.Employee{ID: "helper", CompanyID: "default", Role: "employee"}
	richPost, err := repo.CommunityPost(ctx, u, "post-046")
	richJSON, _ := json.Marshal(richPost.Data["bodyDoc"])
	if err != nil || !strings.Contains(string(richJSON), `"type":"bold"`) {
		t.Fatal("rich document did not survive PostgreSQL restart")
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := repo.CommunityPage(ctx, u, CommunityFilter{Limit: 7, Cursor: cursor, Search: "Guide"})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 7 {
			t.Fatal("unbounded community page")
		}
		for _, r := range page.Items {
			if seen[r.ID] {
				t.Fatal("duplicate community page")
			}
			seen[r.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 47 {
		t.Fatalf("dropped posts: %d", len(seen))
	}
	summary, err := repo.CommunitySummary(ctx, u, now)
	if err != nil || summary.Stats.Points != 50 || summary.Stats.BestStreak != 5 || len(summary.Leaders) != 1 {
		t.Fatalf("SQL stats mismatch: %+v %v", summary, err)
	}
	badges, err := repo.CommunityBadges(ctx, u, []string{"helper", "questioner", "missing"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"resolve-1", "resolve-3", "resolve-5", "streak-5"} {
		found := false
		for _, badge := range badges["helper"] {
			if !badge.Unlocked {
				t.Fatal("locked badge exposed")
			}
			if badge.ID == wanted {
				found = true
			}
		}
		if !found {
			t.Fatalf("earned badge missing: %s", wanted)
		}
	}
	if _, ok := badges["missing"]; ok {
		t.Fatal("unknown author exposed")
	}
	otherBadges, err := repo.CommunityBadges(ctx, domain.Employee{ID: "outsider", CompanyID: "other"}, []string{"helper", "questioner"}, now)
	if err != nil || len(otherBadges) != 0 {
		t.Fatal("cross-company badges exposed")
	}
	b, _ := json.Marshal(summary.Leaders)
	if strings.Contains(string(b), "private") || strings.Contains(string(b), "99999") {
		t.Fatal("leaderboard leaked private employee data")
	}
	page, err := repo.CommunityPage(ctx, u, CommunityFilter{PostID: "post-000", AnswerID: "answer-post-000", Limit: 1})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("pinned answer missing: %v", err)
	}
	other := domain.Employee{ID: "outsider", CompanyID: "other", Role: "owner"}
	page, err = repo.CommunityPage(ctx, other, CommunityFilter{Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("cross-tenant posts exposed")
	}
	summary, err = repo.CommunitySummary(ctx, other, now)
	if err != nil || summary.Stats.Points != 0 || len(summary.Leaders) != 0 {
		t.Fatal("cross-tenant rewards exposed")
	}
	generic, err := repo.Page(ctx, domain.Employee{ID: "owner", CompanyID: "default", Role: "owner"}, "records", PageFilter{Limit: 100})
	if err != nil || len(generic.Items) != 0 {
		t.Fatal("community records exposed in generic API")
	}
	files, err := repo.Page(ctx, u, "files", PageFilter{Limit: 10})
	if err != nil || len(files.Items) != 1 {
		t.Fatal("colleague profile photo missing")
	}
	err = repo.Update(ctx, func(s *domain.State) error {
		for n := 0; n < 47; n++ {
			item := domain.ShopItem{ID: fmt.Sprintf("reward-%03d", n), Title: fmt.Sprintf("Reward %03d", n), Kind: "voucher", Price: 10, Stock: -1, Active: true}
			s.Records = append(s.Records, domain.ShopItemRecord(item, u, now.UTC().Format(time.RFC3339Nano)))
		}
		s.Operations = append(s.Operations, domain.Operation{ID: "purchase-one", CompanyID: "default", EmployeeID: u.ID, Kind: "shop-purchase", Status: "pending", CreatedAt: now.UTC().Format(time.RFC3339Nano), Data: map[string]any{"price": float64(30), "itemId": "reward-001", "title": "Reward 001"}})
		for n := 0; n < 75; n++ {
			s.Operations = append(s.Operations, domain.Operation{ID: fmt.Sprintf("notice-%03d", n), CompanyID: "default", EmployeeID: u.ID, Kind: "notification", Status: "unread", CreatedAt: now.Add(time.Duration(n) * time.Second).UTC().Format("2006-01-02T15:04:05.000000000Z"), Data: map[string]any{"title": "Synthetic update"}})
		}
		s.Records = append(s.Records, domain.Record{ID: "record-percent", CompanyID: "default", EmployeeID: u.ID, Kind: "benefit", Title: "Special 10% benefit", Status: "active", Data: map[string]any{}}, domain.Record{ID: "record-team-leave", CompanyID: "default", EmployeeID: "questioner", Kind: "leave", Title: "Private medical phrase", Status: "approved", Data: map[string]any{"type": "sick", "reason": "private"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := repo.ShopWallet(ctx, u)
	if err != nil || wallet.Earned != 50 || wallet.Spent != 30 || wallet.Balance != 20 {
		t.Fatalf("SQL wallet mismatch: %+v %v", wallet, err)
	}
	err = repo.Update(ctx, func(s *domain.State) error {
		s.Company(u.CompanyID).WorkspaceStatus = domain.WorkspaceStatus{Mode: "maintenance", Message: "Synthetic maintenance", MessageTH: "ข้อความทดสอบ"}
		for n := range 23 {
			s.Operations = append(s.Operations, domain.Operation{ID: fmt.Sprintf("adjust-%03d", n), CompanyID: domain.CompanyID(u.CompanyID), EmployeeID: u.ID, Kind: "points-adjustment", Status: "applied", CreatedAt: now.UTC().Format(time.RFC3339Nano), Data: map[string]any{"delta": float64(2), "reason": "Synthetic recognition"}})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wallet, err = repo.ShopWallet(ctx, u)
	if err != nil || wallet.Adjusted != 46 || wallet.Balance != 66 {
		t.Fatalf("SQL adjustments mismatch: %+v %v", wallet, err)
	}
	var adjustmentIDs = map[string]bool{}
	adjustCursor := ""
	for {
		p, err := repo.PointAdjustments(ctx, u, u.ID, adjustCursor, 5)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range p.Items {
			if adjustmentIDs[op.ID] {
				t.Fatal("duplicate adjustment page")
			}
			adjustmentIDs[op.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		adjustCursor = p.NextCursor
	}
	if len(adjustmentIDs) != 23 {
		t.Fatal("adjustments lost across pages")
	}
	foreignPoints, err := repo.ShopWallet(ctx, domain.Employee{ID: u.ID, CompanyID: "other"})
	if err != nil || foreignPoints.Balance != 0 {
		t.Fatal("points leaked across companies")
	}
	foreignAdjustments, err := repo.PointAdjustments(ctx, other, u.ID, "", 5)
	if err != nil || len(foreignAdjustments.Items) != 0 {
		t.Fatal("adjustments leaked across companies")
	}
	company, err := repo.CompanyByID(ctx, u.CompanyID)
	if err != nil || company.WorkspaceStatus.Mode != "maintenance" || company.WorkspaceStatus.MessageTH == "" {
		t.Fatal("company status did not persist")
	}
	shopSeen := map[string]bool{}
	cursor = ""
	for {
		shop, err := repo.ShopPage(ctx, u, PageFilter{Limit: 7, Cursor: cursor, Status: "active"})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range shop.Items {
			if shopSeen[item.ID] {
				t.Fatal("duplicate reward page")
			}
			shopSeen[item.ID] = true
		}
		if shop.NextCursor == "" {
			break
		}
		cursor = shop.NextCursor
	}
	if len(shopSeen) != 47 {
		t.Fatal("shop pagination dropped rewards")
	}
	queue, err := repo.ShopPurchases(ctx, domain.Employee{ID: "owner", CompanyID: "default", Role: "owner"}, true, "", 5)
	if err != nil || len(queue.Items) != 1 || queue.Items[0].EmployeeName != "Helper" {
		t.Fatal("owner fulfillment queue missing")
	}
	foreign, err := repo.ShopPurchases(ctx, other, true, "", 5)
	if err != nil || len(foreign.Items) != 0 {
		t.Fatal("foreign purchase exposed")
	}
	foreignShop, err := repo.ShopPage(ctx, other, PageFilter{Limit: 20})
	if err != nil || len(foreignShop.Items) != 0 {
		t.Fatal("foreign catalog exposed")
	}
	noticeSeen := map[string]bool{}
	cursor = ""
	for {
		page, err := repo.Notifications(ctx, u, cursor, 9)
		if err != nil || page.UnreadCount != 75 {
			t.Fatal("paged unread total mismatch")
		}
		for _, op := range page.Items {
			if noticeSeen[op.ID] {
				t.Fatal("duplicate notification")
			}
			noticeSeen[op.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(noticeSeen) != 75 {
		t.Fatal("notification paging dropped rows")
	}
	foreignNotifications, err := repo.Notifications(ctx, other, "", 10)
	if err != nil || foreignNotifications.UnreadCount != 0 || len(foreignNotifications.Items) != 0 {
		t.Fatal("foreign notifications exposed")
	}
	lookup, err := repo.Page(ctx, u, "records", PageFilter{Limit: 1, RecordID: "record-percent"})
	if err != nil || len(lookup.Items) != 1 {
		t.Fatal("record target lookup failed")
	}
	literal, err := repo.Page(ctx, u, "records", PageFilter{Limit: 10, Search: "%"})
	if err != nil || len(literal.Items) != 1 {
		t.Fatal("search treated literal percent as SQL wildcard")
	}
	private, err := repo.Page(ctx, u, "records", PageFilter{Limit: 10, Search: "Private medical"})
	if err != nil || len(private.Items) != 0 {
		t.Fatal("search leaked a masked leave title")
	}
	row := domain.HolidayImportRow{Date: "2026-01-01", Day: "th", NameEN: "Imported New Year", NameTH: "วันหยุดปีใหม่", Action: "create"}
	err = repo.Update(domain.WithTenant(ctx, u.CompanyID), func(s *domain.State) error {
		s.Records = append(s.Records, domain.ImportedHolidayRecord(row, u, "sql-import-holiday", now.UTC().Format(time.RFC3339Nano)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := repo.Read(domain.WithTenant(ctx, u.CompanyID))
	if err != nil {
		t.Fatal(err)
	}
	calendar := domain.HolidayCalendarImport{Year: 2026, Months: []domain.HolidayImportMonth{{ID: 1, Holidays: []domain.HolidayImportDay{{Date: 1, Day: "th", NameEN: row.NameEN, NameTH: row.NameTH}}}}}
	importPreview, err := domain.PreviewHolidayImport(&persisted, calendar)
	if err != nil || importPreview.Created != 0 || importPreview.Skipped != 1 {
		t.Fatal("persisted holiday import names or duplicate matching lost")
	}
}
