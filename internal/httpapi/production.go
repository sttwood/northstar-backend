package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/store"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func endpoint(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			writeError(w, err)
		}
	}
}
func (a *API) productionRoutes(mux *http.ServeMux) {
	a.communityRoutes(mux)
	public := map[string]func(http.ResponseWriter, *http.Request) error{"GET /api/auth/providers": a.providers, "POST /api/auth/signup": a.signup, "POST /api/auth/mfa/challenge": a.challenge, "GET /api/auth/sso/start": a.ssoStart, "GET /api/auth/sso/callback": a.ssoCallback, "POST /api/auth/password-reset/request": a.resetRequest, "POST /api/auth/password-reset/confirm": a.resetPassword}
	for path, handler := range public {
		mux.HandleFunc(path, endpoint(handler))
	}
	private := map[string]func(http.ResponseWriter, *http.Request) error{"POST /api/invitations": a.invite, "GET /api/security/mfa": a.mfaStatus, "POST /api/security/mfa/enroll": a.mfaEnroll, "POST /api/security/mfa/confirm": a.mfaConfirm, "POST /api/security/mfa/disable": a.mfaDisable, "GET /api/company": a.companyStatus, "PATCH /api/company": a.companyUpdate, "POST /api/companies": a.createCompany, "POST /api/employees/{id}/offboard": a.offboard, "GET /api/notifications": a.notifications, "PATCH /api/notifications/{id}": a.readNotification, "POST /api/integrations/drive/sync": a.driveSync, "POST /api/retention/run": a.retentionRun, "GET /api/v2/bootstrap": a.bootstrapPage, "GET /api/v2/{resource}": a.page, "POST /api/payroll/calculate": a.payrollCalculate}
	for path, handler := range private {
		mux.Handle(path, a.auth(endpoint(handler)))
	}
	mux.Handle("POST /api/payroll/finalize", a.auth(endpoint(a.payrollFinalize)))
	mux.Handle("POST /api/payroll/opening", a.auth(endpoint(a.payrollOpening)))
	mux.Handle("GET /api/statistics", a.auth(endpoint(a.statistics)))
	mux.Handle("PATCH /api/employees/{id}/retention", a.auth(endpoint(a.employeeRetention)))
	mux.Handle("POST /api/operations/{id}/retry", a.auth(endpoint(a.retryOperation)))
}
func (a *API) companyStatus(w http.ResponseWriter, r *http.Request) error {
	s, err := a.store.Read(r.Context())
	if err != nil {
		return err
	}
	u := current(r)
	c := s.Company(u.CompanyID)
	if c == nil {
		return fail(404, "company not found")
	}
	jobs := []map[string]any{}
	invitations := []map[string]any{}
	holds := map[string]bool{}
	if domain.Manager(u) {
		for i := len(s.Operations) - 1; i >= 0; i-- {
			op := s.Operations[i]
			if op.Kind == "offboarding" {
				held, _ := op.Data["legalHold"].(bool)
				holds[op.EmployeeID] = holds[op.EmployeeID] || held
			}
			if len(jobs) < 100 && domain.Contains([]string{"email", "drive-export", "drive-import", "blob-delete", "drive-delete"}, op.Kind) {
				jobs = append(jobs, map[string]any{"id": op.ID, "kind": op.Kind, "status": op.Status, "attempts": op.Attempts, "createdAt": op.CreatedAt, "nextAt": op.NextAt, "delivery": opText(op, "result")})
			}
			if op.Kind == "invitation" && len(invitations) < 100 {
				invitations = append(invitations, map[string]any{"id": op.ID, "email": opText(op, "email"), "status": op.Status, "expiresAt": op.ExpiresAt})
			}
		}
	}
	scanner := "disabled"
	if a.cfg.Scanner != nil {
		scanner = a.cfg.Scanner.Name()
	}
	visible := *c
	visible.ApprovalStages = []string{"owner"}
	storage := a.cfg.Blobs.Name()
	if a.cfg.DisableUploads && !a.cfg.AllowImageUploads {
		storage = "disabled"
	}
	respond(w, 200, map[string]any{"company": &visible, "integrations": map[string]any{"googleSso": a.cfg.OIDC != nil, "smtp": a.cfg.Mailer != nil && a.cfg.MailMode != "preview", "emailDelivery": a.cfg.MailMode, "googleDrive": a.cfg.Drive != nil, "storage": storage, "scanner": scanner, "fileUploads": !a.cfg.DisableUploads, "imageUploads": !a.cfg.DisableUploads || a.cfg.AllowImageUploads, "uploadPolicy": a.cfg.UploadPolicy, "productionProfile": a.cfg.ProductionProfile, "mfa": len(a.cfg.MasterKey) == 32, "production": a.cfg.Production}, "jobs": jobs, "invitations": invitations, "legalHolds": holds})
	return nil
}
func (a *API) companyUpdate(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "owner permission required")
	}
	var body struct {
		Name                      string   `json:"name"`
		Domain                    string   `json:"domain"`
		RequireMFA                bool     `json:"requireMfa"`
		ApprovalStages            []string `json:"approvalStages"`
		WorkdayHours              float64  `json:"workdayHours"`
		WorkdayStart              string   `json:"workdayStart"`
		WorkdayEnd                string   `json:"workdayEnd"`
		RetentionDays             int      `json:"retentionDays"`
		AuditRetentionDays        int      `json:"auditRetentionDays"`
		NotificationRetentionDays int      `json:"notificationRetentionDays"`
		DriveFolderID             string   `json:"driveFolderId"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Name) == "" || len(body.Name) > 200 || body.WorkdayHours <= 0 || body.WorkdayHours > 16 || body.RetentionDays < 30 || body.RetentionDays > 36500 || body.AuditRetentionDays < 30 || body.AuditRetentionDays > 36500 || body.NotificationRetentionDays < 1 || body.NotificationRetentionDays > 3650 {
		return fail(400, "invalid company policy values")
	}
	if body.RequireMFA && len(a.cfg.MasterKey) != 32 {
		return fail(400, "configure MFA encryption before requiring MFA")
	}
	if len(body.ApprovalStages) != 1 || body.ApprovalStages[0] != "owner" {
		return fail(400, "request approvals are owner-only")
	}
	start, e1 := time.Parse("15:04", body.WorkdayStart)
	end, e2 := time.Parse("15:04", body.WorkdayEnd)
	if e1 != nil || e2 != nil || !end.After(start) || body.WorkdayHours > end.Sub(start).Hours() {
		return fail(400, "invalid company working times")
	}
	body.Domain = strings.ToLower(strings.TrimSpace(body.Domain))
	if body.Domain != "" && !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]+\.[a-z]{2,}$`).MatchString(body.Domain) {
		return fail(400, "valid Google Workspace domain required")
	}
	if body.DriveFolderID != "" && !regexp.MustCompile(`^[a-zA-Z0-9_-]{10,200}$`).MatchString(body.DriveFolderID) {
		return fail(400, "invalid Drive folder ID")
	}
	// A shared service account must never synchronize two tenants into the same folder.
	companies, err := a.store.(interface {
		Companies(context.Context) ([]domain.Company, error)
	}).Companies(r.Context())
	if err != nil {
		return err
	}
	for _, c := range companies {
		if c.ID != u.CompanyID && body.DriveFolderID != "" && c.DriveFolderID == body.DriveFolderID {
			return fail(409, "Drive folder is already assigned to another company")
		}
	}
	err = a.store.Update(r.Context(), func(s *domain.State) error {
		c := s.Company(u.CompanyID)
		wasMFA := c.RequireMFA
		if c.DriveFolderID != body.DriveFolderID {
			for _, op := range s.Operations {
				if op.Kind == "drive-mapping" || (domain.Contains([]string{"drive-export", "drive-import"}, op.Kind) && domain.Contains([]string{"queued", "processing"}, op.Status)) {
					return fail(409, "migrate existing managed Drive files before changing the company folder")
				}
			}
		}
		c.Name = strings.TrimSpace(body.Name)
		c.Domain = body.Domain
		c.RequireMFA = body.RequireMFA
		c.ApprovalStages = body.ApprovalStages
		c.WorkdayHours = body.WorkdayHours
		c.WorkdayStart = body.WorkdayStart
		c.WorkdayEnd = body.WorkdayEnd
		c.RetentionDays = body.RetentionDays
		c.AuditRetentionDays = body.AuditRetentionDays
		c.NotificationRetentionDays = body.NotificationRetentionDays
		c.DriveFolderID = body.DriveFolderID
		if !wasMFA && c.RequireMFA {
			s.Sessions = nil
		}
		audit(s, u, "updated company policy", c.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 204, nil)
	return nil
}
func (a *API) createCompany(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "owner permission required")
	}
	var body struct {
		Name   string `json:"name"`
		Slug   string `json:"slug"`
		Domain string `json:"domain"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	body.Slug = strings.ToLower(strings.TrimSpace(body.Slug))
	if len(body.Name) < 2 || len(body.Name) > 200 || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,49}$`).MatchString(body.Slug) {
		return fail(400, "company name and a 3–50 character lowercase slug are required")
	}
	c := domain.DefaultPolicy(id(), body.Name, body.Slug)
	c.Domain = strings.ToLower(strings.TrimSpace(body.Domain))
	owner := u
	owner.ID = id()
	owner.CompanyID = c.ID
	owner.AvatarFileID = ""
	owner.Decoration = domain.Decoration{}
	owner.ManagerID = ""
	owner.Status = "active"
	owner.Salary = 0
	owner.Title = "Workspace owner"
	owner.OffboardedAt = ""
	owner.RetainUntil = ""
	// Creation is a privileged global transaction; no existing tenant rows are edited.
	ctx := domain.WithTenant(r.Context(), c.ID)
	err := a.store.Update(ctx, func(s *domain.State) error {
		s.Companies = append(s.Companies, c)
		s.Employees = append(s.Employees, owner)
		audit(s, owner, "created company", c.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 201, map[string]any{"company": c, "owner": owner, "signInWithCompany": c.Slug})
	return nil
}
func (a *API) offboard(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if !domain.Manager(u) {
		return fail(403, "manager permission required")
	}
	var body struct {
		TransferTo string `json:"transferTo"`
		LegalHold  bool   `json:"legalHold"`
		Reason     string `json:"reason"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	target := r.PathValue("id")
	if target == u.ID {
		return fail(400, "you cannot offboard your own account")
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		employee := s.Employee(target)
		if employee == nil {
			return fail(404, "employee not found")
		}
		if employee.Role == "owner" && u.Role != "owner" {
			return fail(403, "only owners may offboard owners")
		}
		if !domain.Active(*employee) {
			return fail(409, "employee is already offboarded")
		}
		replacement := s.Employee(body.TransferTo)
		if body.TransferTo != "" && (replacement == nil || !domain.Active(*replacement) || replacement.ID == target) {
			return fail(400, "choose an active replacement employee")
		}
		for _, e := range s.Employees {
			if e.ManagerID == target && replacement == nil {
				return fail(400, "choose a replacement for direct reports")
			}
		}
		for _, record := range s.Records {
			if record.Kind == "task" && record.EmployeeID == target && record.Status != "done" && replacement == nil {
				return fail(400, "choose a replacement for unfinished tasks")
			}
			if domain.Contains(domain.Strings(record, "currentApproverIds"), target) && replacement == nil {
				return fail(400, "choose a replacement for pending approvals")
			}
		}
		for i := range s.Employees {
			if s.Employees[i].ManagerID == target {
				s.Employees[i].ManagerID = body.TransferTo
			}
		}
		for i := range s.Records {
			record := &s.Records[i]
			if record.Kind == "task" && record.EmployeeID == target && record.Status != "done" {
				record.EmployeeID = body.TransferTo
				record.Data["assignee"] = body.TransferTo
				for j := range s.Records {
					p := &s.Records[j]
					if p.ID == domain.Text(*record, "projectId") {
						members := domain.Strings(*p, "members")
						if !domain.Contains(members, body.TransferTo) {
							p.Data["members"] = append(members, body.TransferTo)
						}
					}
				}
			}
			if record.Status == "pending" && domain.RequestKind(record.Kind) {
				steps := domain.ApprovalSteps(*record)
				changed := false
				for j := range steps {
					if steps[j].Status == "pending" {
						for k, id := range steps[j].Approvers {
							if id == target {
								steps[j].Approvers[k] = body.TransferTo
								changed = true
							}
						}
					}
				}
				if changed {
					index := int(domain.Number(*record, "approvalIndex"))
					if index >= 0 && index < len(steps) {
						record.Data["approvalSteps"] = steps
						record.Data["currentApproverIds"] = steps[index].Approvers
					}
				}
			}
		}
		employee = s.Employee(target)
		employee.Status = "offboarded"
		employee.OffboardedAt = stamp()
		employee.RetainUntil = time.Now().AddDate(0, 0, s.Company(u.CompanyID).RetentionDays).UTC().Format(time.RFC3339Nano)
		revokeSessions(s, target)
		s.Operations = append(s.Operations, domain.Operation{ID: id(), CompanyID: u.CompanyID, EmployeeID: target, Kind: "offboarding", Status: "retained", CreatedAt: stamp(), Data: map[string]any{"legalHold": body.LegalHold, "reason": body.Reason, "transferTo": body.TransferTo}})
		audit(s, u, "offboarded employee", target)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 204, nil)
	return nil
}
func (a *API) page(w http.ResponseWriter, r *http.Request) error {
	resource := r.PathValue("resource")
	if resource == "audit" && !domain.Manager(current(r)) {
		return fail(403, "manager permission required")
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 200 {
			return fail(400, "limit must be between 1 and 200")
		}
		limit = n
	}
	q := r.URL.Query()
	if len(q.Get("search")) > 200 || len(q.Get("cursor")) > 100 {
		return fail(400, "query is too long")
	}
	if !domain.Contains([]string{"employees", "records", "files", "audit"}, resource) {
		return fail(404, "resource not found")
	}
	result, err := a.store.(store.Pager).Page(r.Context(), current(r), resource, store.PageFilter{Kind: q.Get("kind"), Status: q.Get("status"), EmployeeID: q.Get("employeeId"), ProjectID: q.Get("projectId"), RecordID: q.Get("recordId"), Environment: q.Get("environment"), Search: q.Get("search"), Cursor: q.Get("cursor"), Limit: limit, Approvals: q.Get("approvals") == "true"})
	if err != nil {
		return err
	}
	respond(w, 200, result)
	return nil
}
func (a *API) bootstrapPage(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	c, err := a.companyByID(r.Context(), u.CompanyID)
	if err != nil {
		return err
	}
	out := map[string]any{"me": u, "company": c}
	cursors := map[string]string{}
	for _, resource := range []string{"employees", "records", "files", "audit"} {
		if resource == "audit" && !domain.Manager(u) {
			out[resource] = []any{}
			continue
		}
		page, err := a.store.(store.Pager).Page(r.Context(), u, resource, store.PageFilter{Limit: 200})
		if err != nil {
			return err
		}
		out[resource] = page.Items
		cursors[resource] = page.NextCursor
	}
	out["cursors"] = cursors
	stats, err := a.store.(interface {
		Statistics(context.Context, domain.Employee, int, int) (store.Statistics, error)
	}).Statistics(r.Context(), u, time.Now().Year(), int(time.Now().Month()))
	if err != nil {
		return err
	}
	out["statistics"] = stats
	respond(w, 200, out)
	return nil
}
func (a *API) companyByID(ctx context.Context, id string) (*domain.Company, error) {
	c, err := a.store.(store.CompanyLookup).CompanyByID(ctx, id)
	return &c, err
}
func (a *API) queueMail(s *domain.State, email, subject, body string) error {
	if a.cfg.Mailer == nil {
		return nil
	}
	payload, err := a.seal(body)
	if err != nil {
		return err
	}
	s.Operations = append(s.Operations, domain.Operation{ID: id(), CompanyID: domain.TenantCompany(s), Kind: "email", Status: "queued", CreatedAt: stamp(), NextAt: stamp(), Data: map[string]any{"to": email, "subject": subject, "body": payload}})
	return nil
}
func (a *API) notifyRecord(s *domain.State, actor domain.Employee, record domain.Record, event string) {
	if !domain.RequestKind(record.Kind) && record.Kind != "ticket" {
		return
	}
	targets := []string{}
	if record.Status == "pending" {
		for _, employee := range s.Employees {
			if domain.Manager(employee) && employee.ID != record.EmployeeID && domain.Active(employee) {
				targets = append(targets, employee.ID)
			}
		}
	}
	if event != "submitted" {
		targets = append(targets, record.EmployeeID)
	}
	if len(targets) == 0 && event == "submitted" {
		for _, e := range s.Employees {
			if domain.Manager(e) {
				targets = append(targets, e.ID)
			}
		}
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if target == actor.ID || seen[target] {
			continue
		}
		seen[target] = true
		employee := s.Employee(target)
		if employee == nil || !domain.Active(*employee) {
			continue
		}
		label := map[string]string{"leave": "Time off", "overtime": "Overtime", "document": "Document", "request": "General"}[record.Kind]
		if label == "" {
			label = record.Kind
		}
		decision := event
		if event == "updated" {
			if record.Status == "approved" {
				decision = "approved"
			}
			if record.Status == "rejected" {
				decision = "declined"
			}
		}
		title := fmt.Sprintf("%s request %s", label, decision)
		op := domain.Operation{ID: id(), CompanyID: record.CompanyID, EmployeeID: target, Kind: "notification", Status: "unread", CreatedAt: stamp(), Data: map[string]any{"title": title, "recordId": record.ID, "kind": record.Kind, "status": record.Status}}
		s.Operations = append(s.Operations, op)
		_ = a.queueMail(s, employee.Email, "Northstar: "+title, "There is an update in your workspace.\n\nOpen Northstar: "+a.cfg.AppURL+"\n\nSign in to view the details.")
	}
}
func (a *API) notifications(w http.ResponseWriter, r *http.Request) error {
	page, err := a.store.(store.NotificationRepository).Notifications(r.Context(), current(r), r.URL.Query().Get("cursor"), communityLimit(r))
	if err != nil {
		if err.Error() == "invalid notification cursor" {
			return fail(400, err.Error())
		}
		return err
	}
	respond(w, 200, page)
	return nil
}
func (a *API) readNotification(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		for i := range s.Operations {
			op := &s.Operations[i]
			if (op.ID == r.PathValue("id") || r.PathValue("id") == "all") && op.Kind == "notification" && op.EmployeeID == u.ID {
				op.Status = "read"
				if r.PathValue("id") != "all" {
					return nil
				}
			}
		}
		if r.PathValue("id") == "all" {
			return nil
		}
		return fail(404, "notification not found")
	})
	if err != nil {
		return err
	}
	respond(w, 204, nil)
	return nil
}
func (a *API) driveSync(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if !domain.Manager(u) {
		return fail(403, "manager permission required")
	}
	if a.cfg.Drive == nil {
		return fail(503, "Google Drive is not configured")
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		c := s.Company(u.CompanyID)
		if c.DriveFolderID == "" {
			return fail(400, "configure a company Drive folder first")
		}
		for _, f := range s.Files {
			if f.Category == "payslip" || f.Category == "document" {
				a.queueDrive(s, f)
			}
		}
		queueJob(s, u.CompanyID, "drive-import", map[string]any{"folder": c.DriveFolderID, "cursor": ""})
		audit(s, u, "requested Drive synchronization", c.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 202, map[string]string{"status": "queued"})
	return nil
}
func queueJob(s *domain.State, company, kind string, data map[string]any) {
	s.Operations = append(s.Operations, domain.Operation{ID: id(), CompanyID: domain.CompanyID(company), Kind: kind, Status: "queued", CreatedAt: stamp(), NextAt: stamp(), Data: data})
}
func fileKey(f domain.File) string {
	if f.StorageKey != "" {
		return f.StorageKey
	}
	return f.ID
}
func (a *API) queueDrive(s *domain.State, f domain.File) {
	c := s.Company(f.CompanyID)
	if a.cfg.Drive == nil || c == nil || c.DriveFolderID == "" {
		return
	}
	for _, op := range s.Operations {
		if domain.Contains([]string{"drive-mapping", "drive-export"}, op.Kind) && opText(op, "fileId") == f.ID && op.Status != "failed" {
			return
		}
	}
	queueJob(s, f.CompanyID, "drive-export", map[string]any{"fileId": f.ID, "folder": c.DriveFolderID})
}
func (a *API) retentionRun(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "owner permission required")
	}
	count, err := a.applyRetention(r.Context(), u.CompanyID)
	if err != nil {
		return err
	}
	respond(w, 200, map[string]any{"purgedEmployees": count})
	return nil
}

func (a *API) statistics(w http.ResponseWriter, r *http.Request) error {
	year, month := time.Now().Year(), int(time.Now().Month())
	if r.URL.Query().Get("year") != "" {
		v, err := strconv.Atoi(r.URL.Query().Get("year"))
		if err != nil || v < 2000 || v > 2200 {
			return fail(400, "invalid year")
		}
		year = v
	}
	if r.URL.Query().Get("month") != "" {
		v, err := strconv.Atoi(r.URL.Query().Get("month"))
		if err != nil || v < 1 || v > 12 {
			return fail(400, "invalid month")
		}
		month = v
	}
	stats, err := a.store.(interface {
		Statistics(context.Context, domain.Employee, int, int) (store.Statistics, error)
	}).Statistics(r.Context(), current(r), year, month)
	if err != nil {
		return err
	}
	respond(w, 200, stats)
	return nil
}
func (a *API) employeeRetention(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "owner permission required")
	}
	var body struct {
		LegalHold bool `json:"legalHold"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		e := s.Employee(r.PathValue("id"))
		if e == nil || e.Status != "offboarded" {
			return fail(400, "offboarded employee required")
		}
		for i := range s.Operations {
			if s.Operations[i].Kind == "offboarding" && s.Operations[i].EmployeeID == e.ID {
				s.Operations[i].Data["legalHold"] = body.LegalHold
			}
		}
		audit(s, u, "changed employee legal hold", e.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 204, nil)
	return nil
}
func (a *API) retryOperation(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if !domain.Manager(u) {
		return fail(403, "manager permission required")
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		for i := range s.Operations {
			op := &s.Operations[i]
			if op.ID != r.PathValue("id") {
				continue
			}
			if !domain.Contains([]string{"email", "drive-export", "drive-import", "blob-delete", "drive-delete"}, op.Kind) || op.Status != "failed" {
				return fail(409, "only failed delivery jobs can be retried")
			}
			op.Status = "queued"
			op.Attempts = 0
			op.NextAt = stamp()
			audit(s, u, "retried delivery", op.ID)
			return nil
		}
		return fail(404, "job not found")
	})
	if err != nil {
		return err
	}
	respond(w, 204, nil)
	return nil
}
