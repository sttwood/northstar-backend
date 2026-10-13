package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"northstar/backend/internal/domain"
	"strings"
	"time"
)

type payrollBody struct {
	EmployeeID              string   `json:"employeeId"`
	Year                    int      `json:"year"`
	Month                   int      `json:"month"`
	Bonus                   float64  `json:"bonus"`
	TaxableBenefits         float64  `json:"taxableBenefits"`
	Reimbursements          float64  `json:"reimbursements"`
	OtherDeductions         float64  `json:"otherDeductions"`
	Insured                 bool     `json:"insured"`
	SocialSecurityWage      *float64 `json:"socialSecurityWage"`
	ProvidentEmployeeRate   float64  `json:"providentEmployeeRate"`
	ProvidentEmployerRate   float64  `json:"providentEmployerRate"`
	SpouseNoIncome          bool     `json:"spouseNoIncome"`
	Children30              int      `json:"children30"`
	Children60              int      `json:"children60"`
	Parents                 int      `json:"parents"`
	CertifiedOtherAllowance float64  `json:"certifiedOtherAllowance"`
}

func calculatePayroll(s *domain.State, body payrollBody) (domain.PayrollInput, domain.PayrollResult, error) {
	input := domain.PayrollInput{Year: body.Year, Month: body.Month, Bonus: body.Bonus, TaxableBenefits: body.TaxableBenefits, Reimbursements: body.Reimbursements, OtherDeductions: body.OtherDeductions, Insured: body.Insured, ProvidentEmployeeRate: body.ProvidentEmployeeRate, ProvidentEmployerRate: body.ProvidentEmployerRate, SpouseNoIncome: body.SpouseNoIncome, Children30: body.Children30, Children60: body.Children60, Parents: body.Parents, CertifiedOtherAllowance: body.CertifiedOtherAllowance}
	employee := s.Employee(body.EmployeeID)
	if employee == nil || !domain.Active(*employee) {
		return input, domain.PayrollResult{}, fail(400, "active employee required")
	}
	company := s.Company(employee.CompanyID)
	if company == nil || company.Country != "TH" || company.TaxYear != 2026 {
		return input, domain.PayrollResult{}, fail(400, "only Thailand tax year 2026 is configured")
	}
	input.Salary = employee.Salary
	input.SocialSecurityWage = employee.Salary
	if body.SocialSecurityWage != nil {
		input.SocialSecurityWage = *body.SocialSecurityWage
	}
	covered := map[int]bool{}
	if started, err := time.Parse("2006-01-02", employee.StartDate); err == nil && started.Year() == body.Year {
		if body.Month < int(started.Month()) {
			return input, domain.PayrollResult{}, fail(400, "pay period precedes employment start")
		}
		for month := 1; month < int(started.Month()); month++ {
			covered[month] = true
		}
	}
	for _, op := range s.Operations {
		if op.Kind == "payroll-opening" && op.EmployeeID == employee.ID {
			b, _ := json.Marshal(op.Data)
			var opening struct {
				Year, ThroughMonth              int
				Gross, Withheld, SSO, Provident float64
			}
			_ = json.Unmarshal(b, &opening)
			if opening.Year == body.Year && opening.ThroughMonth < body.Month {
				input.YTDGross = opening.Gross
				input.YTDWithheld = opening.Withheld
				input.YTDSSO = opening.SSO
				input.YTDProvident = opening.Provident
				for month := 1; month <= opening.ThroughMonth; month++ {
					covered[month] = true
				}
			}
		}
	}
	for _, record := range s.Records {
		if record.Kind == "payroll" && record.EmployeeID == employee.ID && int(domain.Number(record, "year")) == body.Year {
			month := int(domain.Number(record, "month"))
			if month == body.Month {
				return input, domain.PayrollResult{}, fail(409, "payroll is already finalized for this period")
			}
			if month < body.Month && !covered[month] {
				input.YTDGross += domain.Number(record, "gross")
				input.YTDWithheld += domain.Number(record, "withholding")
				input.YTDSSO += domain.Number(record, "employeeSso")
				input.YTDProvident += domain.Number(record, "providentEmployee")
				covered[month] = true
			}
		}
	}
	for month := 1; month < body.Month; month++ {
		if !covered[month] {
			return input, domain.PayrollResult{}, fail(409, "record opening payroll balances or finalize earlier months before calculating this period")
		}
	}
	prefix := fmt.Sprintf("%04d-%02d", body.Year, body.Month)
	for _, record := range s.Records {
		if record.Kind == "overtime" && record.EmployeeID == employee.ID && record.Status == "approved" && strings.HasPrefix(domain.Text(record, "date"), prefix) {
			day, err := time.Parse("2006-01-02", domain.Text(record, "date"))
			if err != nil {
				continue
			}
			rate := 1.5
			if domain.Workdays(s, *employee, day, day) == 0 {
				rate = 3
			}
			input.Overtime += employee.Salary / 30 / company.WorkdayHours * domain.Number(record, "hours") * rate
		}
	}
	result, err := domain.CalculateThaiPayroll(input)
	if err != nil {
		return input, result, fail(400, err.Error())
	}
	return input, result, nil
}
func (a *API) payrollCalculate(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "manager permission required")
	}
	var body payrollBody
	if err := decode(w, r, &body); err != nil {
		return err
	}
	s, err := a.store.Read(r.Context())
	if err != nil {
		return err
	}
	input, result, err := calculatePayroll(&s, body)
	if err != nil {
		return err
	}
	respond(w, 200, map[string]any{"input": input, "result": result})
	return nil
}
func (a *API) payrollFinalize(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "manager permission required")
	}
	var body payrollBody
	if err := decode(w, r, &body); err != nil {
		return err
	}
	var record domain.Record
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		input, result, err := calculatePayroll(s, body)
		if err != nil {
			return err
		}
		b, _ := json.Marshal(result)
		values := map[string]any{}
		_ = json.Unmarshal(b, &values)
		values["month"] = float64(body.Month)
		values["year"] = float64(body.Year)
		values["input"] = input
		record = domain.Record{ID: id(), CompanyID: u.CompanyID, EmployeeID: body.EmployeeID, Kind: "payroll", Status: "finalized", Title: fmt.Sprintf("Payroll %04d-%02d", body.Year, body.Month), CreatedAt: stamp(), Data: values}
		s.Records = append(s.Records, record)
		audit(s, u, "finalized payroll", record.ID)
		employee := s.Employee(body.EmployeeID)
		return a.queueMail(s, employee.Email, "Northstar: payroll finalized", "Your payroll has been finalized. Sign in to view your payslip: "+a.cfg.AppURL)
	})
	if err != nil {
		return err
	}
	respond(w, 201, record)
	return nil
}
func (a *API) payrollOpening(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "manager permission required")
	}
	var body struct {
		EmployeeID   string  `json:"employeeId"`
		Year         int     `json:"year"`
		ThroughMonth int     `json:"throughMonth"`
		Gross        float64 `json:"gross"`
		Withheld     float64 `json:"withheld"`
		SSO          float64 `json:"sso"`
		Provident    float64 `json:"provident"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if body.Year != 2026 || body.ThroughMonth < 1 || body.ThroughMonth > 11 || body.Gross < 0 || body.Withheld < 0 || body.SSO < 0 || body.Provident < 0 || body.Withheld > body.Gross {
		return fail(400, "invalid opening payroll balances")
	}
	b, _ := json.Marshal(body)
	data := map[string]any{}
	_ = json.Unmarshal(b, &data)
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if s.Employee(body.EmployeeID) == nil {
			return fail(400, "employee not found")
		}
		for _, record := range s.Records {
			if record.Kind == "payroll" && record.EmployeeID == body.EmployeeID && int(domain.Number(record, "year")) == body.Year && int(domain.Number(record, "month")) <= body.ThroughMonth {
				return fail(409, "opening balances cannot overlap finalized payroll")
			}
		}
		for _, op := range s.Operations {
			if op.Kind == "payroll-opening" && op.EmployeeID == body.EmployeeID {
				return fail(409, "opening balances already exist; use a reviewed correction migration")
			}
		}
		s.Operations = append(s.Operations, domain.Operation{ID: id(), CompanyID: u.CompanyID, EmployeeID: body.EmployeeID, Kind: "payroll-opening", Status: "active", CreatedAt: stamp(), Data: data})
		audit(s, u, "recorded payroll opening balances", body.EmployeeID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 201, map[string]any{"saved": true})
	return nil
}
