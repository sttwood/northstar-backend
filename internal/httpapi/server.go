package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"io"
	"log/slog"
	"net/http"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/integrations"
	"northstar/backend/internal/store"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Origins           []string
	UploadDir         string
	SecureCookie      bool
	Production        bool
	ProductionProfile string
	DisableUploads    bool
	UploadPolicy      string
	AllowImageUploads bool
	FrontendURL       string
	MailMode          string
	AppURL            string
	APIURL            string
	MasterKey         []byte
	Mailer            integrations.Mailer
	Blobs             integrations.BlobStore
	Scanner           integrations.Scanner
	OIDC              *oidc.Provider
	OAuth             oauth2.Config
	Drive             *integrations.Drive
}
type API struct {
	store    store.Store
	cfg      Config
	mu       sync.Mutex
	attempts map[string][]time.Time
}
type ctxKey struct{}
type apiError struct {
	code    int
	message string
}

func (e apiError) Error() string          { return e.message }
func fail(code int, message string) error { return apiError{code, message} }
func New(s store.Store, cfg Config) http.Handler {
	_, handler := NewService(s, cfg)
	return handler
}
func NewService(s store.Store, cfg Config) (*API, http.Handler) {
	if cfg.Blobs == nil {
		cfg.Blobs = integrations.Local{Dir: cfg.UploadDir}
	}
	a := &API{store: s, cfg: cfg, attempts: map[string][]time.Time{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.(interface{ Ping(context.Context) error }).Ping(r.Context()); err != nil {
			writeError(w, fail(503, "database unavailable"))
			return
		}
		respond(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.Handle("POST /api/auth/logout", a.auth(http.HandlerFunc(a.logout)))
	mux.Handle("POST /api/auth/password", a.auth(http.HandlerFunc(a.password)))
	mux.Handle("GET /api/bootstrap", a.auth(http.HandlerFunc(a.bootstrap)))
	mux.Handle("POST /api/records", a.auth(http.HandlerFunc(a.createRecord)))
	mux.Handle("PATCH /api/records/{id}", a.auth(http.HandlerFunc(a.patchRecord)))
	mux.Handle("DELETE /api/records/{id}", a.auth(http.HandlerFunc(a.deleteRecord)))
	mux.Handle("PATCH /api/employees/{id}", a.auth(http.HandlerFunc(a.patchEmployee)))
	mux.Handle("POST /api/employees", a.auth(http.HandlerFunc(a.createEmployee)))
	mux.Handle("POST /api/files", a.auth(http.HandlerFunc(a.upload)))
	mux.Handle("GET /api/files/{id}", a.auth(http.HandlerFunc(a.download)))
	mux.Handle("DELETE /api/files/{id}", a.auth(http.HandlerFunc(a.deleteFile)))
	a.productionRoutes(mux)
	a.ownerControlRoutes(mux)
	if cfg.FrontendURL != "" {
		mux.Handle("/", frontendProxy(cfg.FrontendURL))
	}
	return a, a.middleware(mux)
}
func id() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func stamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func respond(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}
func writeError(w http.ResponseWriter, err error) {
	var conflict *pgconn.PgError
	if errors.As(err, &conflict) && conflict.Code == "23505" {
		respond(w, 409, map[string]string{"error": "That value is already in use."})
		return
	}
	var e apiError
	if errors.As(err, &e) {
		respond(w, e.code, map[string]string{"error": e.message})
		return
	}
	slog.Error("request failed", "error", err)
	respond(w, 500, map[string]string{"error": "The request could not be completed. Please try again."})
}
func decode(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fail(400, "invalid JSON request")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fail(400, "request must contain exactly one JSON value")
	}
	return nil
}
func current(r *http.Request) domain.Employee { return r.Context().Value(ctxKey{}).(domain.Employee) }
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func token(r *http.Request) string {
	if c, err := r.Cookie("northstar_session"); err == nil {
		return c.Value
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
func (a *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookup := a.store.(store.IdentityLookup)
		u, _, err := lookup.SessionUser(r.Context(), tokenHash(token(r)))
		if err == nil && domain.Active(u) {
			ctx := domain.WithTenant(r.Context(), u.CompanyID)
			if !a.checkWorkspaceAccess(w, r.WithContext(ctx), u) {
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxKey{}, u)))
			return
		}
		writeError(w, fail(401, "Sign in to access your workspace."))
	})
}
func (a *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", id())
		w.Header().Set("Vary", "Origin")
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "value", v)
				writeError(w, fail(500, "unexpected server error"))
			}
		}()
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !domain.Contains(a.cfg.Origins, origin) {
				writeError(w, fail(403, "origin is not allowed"))
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		if r.Method != "GET" && origin == "" && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, fail(403, "cross-site request denied"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *API) cookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "northstar_session", Value: value, Path: "/api", HttpOnly: true, Secure: a.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Company  string `json:"company"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	key := email
	a.mu.Lock()
	recent := []time.Time{}
	for _, t := range a.attempts[key] {
		if time.Since(t) < 15*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(a.attempts) > 10000 {
		a.attempts = map[string][]time.Time{}
	}
	a.attempts[key] = append(recent, time.Now())
	blocked := len(recent) >= 10
	a.mu.Unlock()
	if blocked {
		writeError(w, fail(429, "Too many sign-in attempts. Try again in 15 minutes."))
		return
	}
	users, err := a.store.(store.IdentityLookup).LoginUsers(r.Context(), email, strings.ToLower(strings.TrimSpace(body.Company)))
	if err != nil {
		writeError(w, err)
		return
	}
	var found *domain.Employee
	if len(users) == 1 && domain.Active(users[0]) {
		found = &users[0]
	}
	if found == nil {
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoO5SWQ5ZlvFjRv4RlBNjCkRb8yO2abQvW"), []byte(body.Password))
		writeError(w, fail(401, "Email or password is incorrect."))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(found.PasswordHash), []byte(body.Password)) != nil {
		writeError(w, fail(401, "Email or password is incorrect."))
		return
	}
	a.mu.Lock()
	delete(a.attempts, key)
	a.mu.Unlock()
	a.finishLogin(w, r, *found, "password", false)
}
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	hash := tokenHash(token(r))
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		list := []domain.Session{}
		for _, v := range s.Sessions {
			if v.Hash != hash {
				list = append(list, v)
			}
		}
		s.Sessions = list
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	a.cookie(w, "", -1)
	respond(w, 204, nil)
}
func audit(s *domain.State, u domain.Employee, action, target string) {
	s.Audit = append(s.Audit, domain.Audit{ID: id(), CompanyID: domain.CompanyID(u.CompanyID), ActorID: u.ID, Action: action, TargetID: target, CreatedAt: stamp()})
}
func (a *API) bootstrap(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.Read(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	u := current(r)
	users := []domain.Employee{}
	records := []domain.Record{}
	files := []domain.File{}
	logs := []domain.Audit{}
	for _, e := range s.Employees {
		if !domain.Manager(u) && e.ID != u.ID {
			e.Salary = 0
			e.Phone = ""
			e.Location = ""
			e.StartDate = ""
		}
		users = append(users, e)
	}
	for _, v := range s.Records {
		if !domain.CanRead(&s, u, v) {
			continue
		}
		if v.Kind == "leave" && v.EmployeeID != u.ID && !domain.Manager(u) {
			v.Title = "Team leave"
			v.Data = map[string]any{"type": v.Data["type"], "startDate": v.Data["startDate"], "endDate": v.Data["endDate"], "days": v.Data["days"]}
		}
		records = append(records, v)
	}
	for _, f := range s.Files {
		if domain.CanReadFile(&s, u, f) {
			files = append(files, f)
		}
	}
	if domain.Manager(u) {
		logs = s.Audit
	}
	respond(w, 200, map[string]any{"me": u, "company": s.Company(u.CompanyID), "employees": users, "records": records, "files": files, "audit": logs})
}
func (a *API) createRecord(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	var item domain.Record
	if err := decode(w, r, &item); err != nil {
		writeError(w, err)
		return
	}
	item.ID = id()
	item.CompanyID = domain.CompanyID(u.CompanyID)
	item.CreatedAt = stamp()
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := domain.Validate(s, u, &item, nil); err != nil {
			return fail(400, err.Error())
		}
		if err := domain.InitializeApproval(s, &item); err != nil {
			return fail(400, err.Error())
		}
		s.Records = append(s.Records, item)
		a.notifyRecord(s, u, item, "submitted")
		audit(s, u, "created "+item.Kind, item.ID)
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 201, item)
}
func (a *API) patchRecord(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	var patch struct {
		EmployeeID *string         `json:"employeeId"`
		Title      *string         `json:"title"`
		Status     *string         `json:"status"`
		Data       *map[string]any `json:"data"`
	}
	if err := decode(w, r, &patch); err != nil {
		writeError(w, err)
		return
	}
	var result domain.Record
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		for i, old := range s.Records {
			if old.ID != r.PathValue("id") {
				continue
			}
			if !domain.CanEdit(s, u, old) {
				return fail(403, "you cannot edit this record")
			}
			result = old
			oldData, _ := json.Marshal(old.Data)
			result.Data = map[string]any{}
			_ = json.Unmarshal(oldData, &result.Data)
			if patch.EmployeeID != nil {
				result.EmployeeID = *patch.EmployeeID
			}
			if patch.Title != nil {
				result.Title = *patch.Title
			}
			if patch.Status != nil {
				result.Status = *patch.Status
			}
			if patch.Data != nil {
				result.Data = *patch.Data
			}
			if domain.RequestKind(old.Kind) {
				if domain.CanApprove(s, u, old) && !domain.Manager(u) && old.EmployeeID != u.ID {
					if patch.Title != nil || patch.EmployeeID != nil {
						return fail(403, "approvers may only make a decision")
					}
					note := domain.Text(result, "decisionNote")
					b, _ := json.Marshal(old.Data)
					result.Data = map[string]any{}
					_ = json.Unmarshal(b, &result.Data)
					result.Data["decisionNote"] = note
				}
				domain.PreserveApproval(&result, old)
			}
			if err := domain.Validate(s, u, &result, &old); err != nil {
				return fail(400, err.Error())
			}
			if err := domain.AdvanceApproval(s, u, &result, old); err != nil {
				return fail(403, err.Error())
			}
			s.Records[i] = result
			a.notifyRecord(s, u, result, "updated")
			audit(s, u, "updated "+result.Kind, result.ID)
			return nil
		}
		return fail(404, "record not found")
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, result)
}
func (a *API) deleteRecord(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	target := r.PathValue("id")
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		var found *domain.Record
		for i := range s.Records {
			if s.Records[i].ID == target {
				found = &s.Records[i]
				break
			}
		}
		if found == nil {
			return fail(404, "record not found")
		}
		if !domain.CanEdit(s, u, *found) {
			return fail(403, "you cannot delete this record")
		}
		if !domain.Manager(u) && found.EmployeeID != u.ID && domain.RequestKind(found.Kind) {
			return fail(403, "approvers cannot delete requests")
		}
		if found.Kind == "payroll" {
			return fail(400, "finalized payroll is retained immutably")
		}
		if domain.Contains([]string{"leave", "overtime", "document", "request"}, found.Kind) && found.Status != "pending" {
			return fail(400, "decided requests are retained for audit")
		}
		if found.Kind == "project" {
			for _, p := range s.Records {
				if p.Kind == "project" && domain.Text(p, "parentId") == target {
					return fail(400, "reassign or delete child projects before deleting their parent")
				}
			}
		}
		for _, file := range s.Files {
			linked := file.RecordID == target || file.ProjectID == target
			if found.Kind == "project" {
				for _, item := range s.Records {
					if domain.Text(item, "projectId") == target && file.RecordID == item.ID {
						linked = true
					}
				}
			}
			if linked {
				return fail(400, "delete attached files before deleting the request")
			}
		}
		kept := []domain.Record{}
		for _, v := range s.Records {
			if v.ID != target && domain.Text(v, "projectId") != target {
				deps := domain.Strings(v, "dependsOn")
				clean := []string{}
				for _, dep := range deps {
					if dep != target {
						clean = append(clean, dep)
					}
				}
				if v.Kind == "task" {
					v.Data["dependsOn"] = clean
				}
				kept = append(kept, v)
			}
		}
		s.Records = kept
		audit(s, u, "deleted "+found.Kind, target)
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 204, nil)
}
func (a *API) password(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	if len(body.Password) < 12 || len(body.Password) > 72 {
		writeError(w, fail(400, "password must contain 12–72 bytes"))
		return
	}
	u := current(r)
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(body.Current)) != nil {
		writeError(w, fail(400, "current password is incorrect"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, err)
		return
	}
	err = a.store.Update(r.Context(), func(s *domain.State) error {
		s.Employee(u.ID).PasswordHash = string(hash)
		sessions := []domain.Session{}
		for _, v := range s.Sessions {
			if v.EmployeeID != u.ID {
				sessions = append(sessions, v)
			}
		}
		s.Sessions = sessions
		audit(s, u, "changed password", u.ID)
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	a.cookie(w, "", -1)
	respond(w, 204, nil)
}
func (a *API) patchEmployee(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	target := r.PathValue("id")
	if !domain.Manager(u) && target != u.ID {
		writeError(w, fail(403, "permission denied"))
		return
	}
	var body struct {
		ManagerID      *string  `json:"managerId"`
		Name           *string  `json:"name"`
		Phone          *string  `json:"phone"`
		Location       *string  `json:"location"`
		Salary         *float64 `json:"salary"`
		Team           *string  `json:"team"`
		Title          *string  `json:"title"`
		Role           *string  `json:"role"`
		LeaveAllowance *float64 `json:"leaveAllowance"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		e := s.Employee(target)
		if e == nil {
			return fail(404, "employee not found")
		}
		if !domain.Manager(u) && (body.Salary != nil || body.Team != nil || body.Title != nil || body.Role != nil || body.LeaveAllowance != nil || body.ManagerID != nil) {
			return fail(403, "only managers may change employment details")
		}
		if body.Name != nil {
			if strings.TrimSpace(*body.Name) == "" {
				return fail(400, "name is required")
			}
			e.Name = *body.Name
		}
		if body.ManagerID != nil {
			if err := domain.ValidateManager(s, e.ID, *body.ManagerID); err != nil {
				return fail(400, err.Error())
			}
			e.ManagerID = *body.ManagerID
		}
		if body.Phone != nil {
			e.Phone = *body.Phone
		}
		if body.Location != nil {
			e.Location = *body.Location
		}
		if body.Salary != nil {
			if *body.Salary < 0 {
				return fail(400, "salary cannot be negative")
			}
			e.Salary = *body.Salary
		}
		if body.Team != nil {
			e.Team = *body.Team
		}
		if body.Title != nil {
			e.Title = *body.Title
		}
		if body.LeaveAllowance != nil {
			if *body.LeaveAllowance < 0 {
				return fail(400, "leave allowance cannot be negative")
			}
			e.LeaveAllowance = *body.LeaveAllowance
		}
		if body.Role != nil {
			if u.Role != "owner" {
				return fail(403, "only owners may change roles")
			}
			if !domain.Contains([]string{"employee", "hr", "owner"}, *body.Role) {
				return fail(400, "invalid role")
			}
			if e.ID == u.ID && *body.Role != "owner" {
				return fail(400, "owners cannot remove their own role")
			}
			e.Role = *body.Role
		}
		audit(s, u, "updated employee", target)
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 204, nil)
}
func (a *API) createEmployee(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	if !domain.Manager(u) {
		writeError(w, fail(403, "manager permission required"))
		return
	}
	var body struct {
		ManagerID      string  `json:"managerId"`
		Name           string  `json:"name"`
		Email          string  `json:"email"`
		Password       string  `json:"password"`
		Role           string  `json:"role"`
		Team           string  `json:"team"`
		Title          string  `json:"title"`
		Salary         float64 `json:"salary"`
		StartDate      string  `json:"startDate"`
		LeaveAllowance float64 `json:"leaveAllowance"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, err)
		return
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	if body.Name == "" || !strings.Contains(body.Email, "@") || len(body.Password) < 12 || len(body.Password) > 72 || body.Salary < 0 || body.LeaveAllowance < 0 {
		writeError(w, fail(400, "name, email, nonnegative salary/allowance and a 12–72 byte password are required"))
		return
	}
	if body.Role == "" {
		body.Role = "employee"
	}
	if !domain.Contains([]string{"employee", "hr", "owner"}, body.Role) || (body.Role == "owner" && u.Role != "owner") {
		writeError(w, fail(403, "invalid role assignment"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, err)
		return
	}
	employee := domain.Employee{ID: id(), CompanyID: domain.CompanyID(u.CompanyID), Status: "active", Name: body.Name, Email: body.Email, Role: body.Role, Team: body.Team, Title: body.Title, Salary: body.Salary, StartDate: body.StartDate, LeaveAllowance: body.LeaveAllowance, PasswordHash: string(hash)}
	for _, part := range strings.Fields(body.Name) {
		employee.Avatar += strings.ToUpper(string([]rune(part)[0]))
	}
	err = a.store.Update(r.Context(), func(s *domain.State) error {
		if err := domain.ValidateManager(s, employee.ID, body.ManagerID); err != nil {
			return fail(400, err.Error())
		}
		employee.ManagerID = body.ManagerID
		for _, e := range s.Employees {
			if e.Email == body.Email {
				return fail(409, "email already exists")
			}
		}
		s.Employees = append(s.Employees, employee)
		audit(s, u, "created employee", employee.ID)
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 201, employee)
}
