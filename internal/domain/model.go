package domain

import "time"

type Employee struct {
	CompanyID      string     `json:"companyId,omitempty"`
	ManagerID      string     `json:"managerId,omitempty"`
	Status         string     `json:"status,omitempty"`
	OffboardedAt   string     `json:"offboardedAt,omitempty"`
	RetainUntil    string     `json:"retainUntil,omitempty"`
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Email          string     `json:"email"`
	Role           string     `json:"role"`
	Team           string     `json:"team"`
	Title          string     `json:"title"`
	Phone          string     `json:"phone"`
	Location       string     `json:"location"`
	StartDate      string     `json:"startDate"`
	Salary         float64    `json:"salary,omitempty"`
	LeaveAllowance float64    `json:"leaveAllowance"`
	Avatar         string     `json:"avatar"`
	AvatarFileID   string     `json:"avatarFileId,omitempty"`
	Decoration     Decoration `json:"decoration,omitempty"`
	PasswordHash   string     `json:"-"`
}
type Record struct {
	CompanyID  string         `json:"companyId,omitempty"`
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	EmployeeID string         `json:"employeeId"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	CreatedAt  string         `json:"createdAt"`
	Data       map[string]any `json:"data"`
}
type File struct {
	CompanyID    string `json:"companyId,omitempty"`
	StorageKey   string `json:"storageKey,omitempty"`
	ScanStatus   string `json:"scanStatus,omitempty"`
	ID           string `json:"id"`
	EmployeeID   string `json:"employeeId"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	Month        int    `json:"month"`
	Year         int    `json:"year"`
	Size         int64  `json:"size"`
	CreatedAt    string `json:"createdAt"`
	RecordID     string `json:"recordId,omitempty"`
	ProjectID    string `json:"projectId,omitempty"`
	MimeType     string `json:"mimeType,omitempty"`
	ResourceKind string `json:"resourceKind,omitempty"`
	Description  string `json:"description,omitempty"`
	Environment  string `json:"environment,omitempty"`
}
type Session struct {
	Hash       string
	EmployeeID string
	Expires    time.Time
	Assurance  string
}
type Audit struct {
	CompanyID string `json:"companyId,omitempty"`
	ID        string `json:"id"`
	ActorID   string `json:"actorId"`
	Action    string `json:"action"`
	TargetID  string `json:"targetId"`
	CreatedAt string `json:"createdAt"`
}
type State struct {
	Companies  []Company         `json:"companies,omitempty"`
	Operations []Operation       `json:"-"`
	Security   []AccountSecurity `json:"-"`
	Employees  []Employee        `json:"employees"`
	Records    []Record          `json:"records"`
	Files      []File            `json:"files"`
	Audit      []Audit           `json:"audit"`
	Sessions   []Session         `json:"-"`
}

func (s *State) Employee(id string) *Employee {
	for i := range s.Employees {
		if s.Employees[i].ID == id {
			return &s.Employees[i]
		}
	}
	return nil
}
func Text(r Record, key string) string    { v, _ := r.Data[key].(string); return v }
func Number(r Record, key string) float64 { v, _ := r.Data[key].(float64); return v }
func Strings(r Record, key string) []string {
	result := []string{}
	switch values := r.Data[key].(type) {
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
	case []string:
		return values
	}
	return result
}

// Manager is the legacy administration capability name; only owners receive it.
// Legacy HR accounts have employee permissions until an owner changes their role.
func Manager(u Employee) bool { return u.Role == "owner" }
func Contains(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
func ProjectAccess(s *State, u Employee, id string) bool {
	for _, r := range s.Records {
		if r.Kind == "project" && r.ID == id && CompanyID(r.CompanyID) == CompanyID(u.CompanyID) {
			return Manager(u) || r.EmployeeID == u.ID || Contains(Strings(r, "members"), u.ID)
		}
	}
	return false
}
func CanRead(s *State, u Employee, r Record) bool {
	if CommunityKind(r.Kind) || r.Kind == "shop-item" {
		return false
	}
	if CompanyID(u.CompanyID) != CompanyID(r.CompanyID) {
		return false
	}
	if r.Kind == "template" {
		return Manager(u)
	}
	if CanApprove(s, u, r) {
		return true
	}
	if Manager(u) {
		return true
	}
	switch r.Kind {
	case "holiday", "benefit":
		return true
	case "project":
		return ProjectAccess(s, u, r.ID)
	case "task", "comment":
		return ProjectAccess(s, u, Text(r, "projectId"))
	case "leave":
		return r.EmployeeID == u.ID || r.Status == "approved"
	default:
		return r.EmployeeID == u.ID
	}
}
func CanEdit(s *State, u Employee, r Record) bool {
	if CommunityKind(r.Kind) || r.Kind == "shop-item" {
		return false
	}
	if CompanyID(u.CompanyID) != CompanyID(r.CompanyID) {
		return false
	}
	if Contains([]string{"template", "holiday", "benefit"}, r.Kind) {
		return u.Role == "owner"
	}
	if r.Kind == "onboarding" {
		return u.Role == "owner" || r.EmployeeID == u.ID
	}
	if CanApprove(s, u, r) {
		return true
	}
	if Manager(u) {
		return true
	}
	switch r.Kind {
	case "task":
		return ProjectAccess(s, u, Text(r, "projectId"))
	case "comment":
		return r.EmployeeID == u.ID && ProjectAccess(s, u, Text(r, "projectId"))
	case "onboarding":
		return r.EmployeeID == u.ID
	case "holiday", "benefit", "project", "template":
		return false
	default:
		return r.EmployeeID == u.ID && (r.Status == "pending" || r.Status == "open")
	}
}
