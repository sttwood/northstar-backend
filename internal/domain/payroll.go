package domain

import (
	"errors"
	"math"
)

// Money is calculated in satang; presentation converts to THB only at the boundary.
type PayrollInput struct {
	Year                    int     `json:"year"`
	Month                   int     `json:"month"`
	Salary                  float64 `json:"salary"`
	Overtime                float64 `json:"overtime"`
	Bonus                   float64 `json:"bonus"`
	TaxableBenefits         float64 `json:"taxableBenefits"`
	Reimbursements          float64 `json:"reimbursements"`
	OtherDeductions         float64 `json:"otherDeductions"`
	Insured                 bool    `json:"insured"`
	SocialSecurityWage      float64 `json:"socialSecurityWage"`
	ProvidentEmployeeRate   float64 `json:"providentEmployeeRate"`
	ProvidentEmployerRate   float64 `json:"providentEmployerRate"`
	SpouseNoIncome          bool    `json:"spouseNoIncome"`
	Children30              int     `json:"children30"`
	Children60              int     `json:"children60"`
	Parents                 int     `json:"parents"`
	CertifiedOtherAllowance float64 `json:"certifiedOtherAllowance"`
	YTDGross                float64 `json:"ytdGross"`
	YTDWithheld             float64 `json:"ytdWithheld"`
	YTDSSO                  float64 `json:"ytdSso"`
	YTDProvident            float64 `json:"ytdProvident"`
}
type PayrollResult struct {
	RuleVersion          string  `json:"ruleVersion"`
	Gross                float64 `json:"gross"`
	Overtime             float64 `json:"overtime"`
	EmployeeSSO          float64 `json:"employeeSso"`
	EmployerSSO          float64 `json:"employerSso"`
	ProvidentEmployee    float64 `json:"providentEmployee"`
	ProvidentEmployer    float64 `json:"providentEmployer"`
	ProjectedAnnualGross float64 `json:"projectedAnnualGross"`
	ExpenseDeduction     float64 `json:"expenseDeduction"`
	Allowances           float64 `json:"allowances"`
	TaxableAnnualIncome  float64 `json:"taxableAnnualIncome"`
	AnnualTax            float64 `json:"annualTax"`
	Withholding          float64 `json:"withholding"`
	NetPay               float64 `json:"netPay"`
	EmployerCost         float64 `json:"employerCost"`
}

func satang(v float64) int64 { return int64(math.Round(v * 100)) }
func baht(v int64) float64   { return float64(v) / 100 }
func roundRatio(value, numerator, denominator int64) int64 {
	return (value*numerator + denominator/2) / denominator
}
func ThaiAnnualTax(taxableSatang int64) int64 {
	limits := []int64{150000, 300000, 500000, 750000, 1000000, 2000000, 5000000}
	rates := []int64{0, 5, 10, 15, 20, 25, 30, 35}
	previous, tax := int64(0), int64(0)
	for i, limit := range limits {
		upper := limit * 100
		if taxableSatang <= previous {
			return tax
		}
		part := min(taxableSatang, upper) - previous
		tax += roundRatio(part, rates[i], 100)
		previous = upper
	}
	if taxableSatang > previous {
		tax += roundRatio(taxableSatang-previous, 35, 100)
	}
	return tax
}
func ThaiSocialSecurity(wage float64, insured bool) int64 {
	if !insured || wage <= 0 {
		return 0
	}
	base := max(int64(1650*100), min(satang(wage), int64(17500*100)))
	return roundRatio(base, 5, 10000) * 100
}
func CalculateThaiPayroll(in PayrollInput) (PayrollResult, error) {
	out := PayrollResult{RuleVersion: "TH-2026-40(1)-v1"}
	if in.Year != 2026 || in.Month < 1 || in.Month > 12 {
		return out, errors.New("only Thailand tax year 2026 is configured")
	}
	for _, v := range []float64{in.Salary, in.Overtime, in.Bonus, in.TaxableBenefits, in.Reimbursements, in.OtherDeductions, in.SocialSecurityWage, in.CertifiedOtherAllowance, in.YTDGross, in.YTDWithheld, in.YTDSSO, in.YTDProvident} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100000000 {
			return out, errors.New("payroll values must be nonnegative and within supported limits")
		}
	}
	if in.ProvidentEmployeeRate < 0 || in.ProvidentEmployeeRate > 0.15 || in.ProvidentEmployerRate < 0 || in.ProvidentEmployerRate > 0.15 || in.Children30 < 0 || in.Children60 < 0 || in.Children30+in.Children60 > 20 || in.Parents < 0 || in.Parents > 4 {
		return out, errors.New("invalid certified allowances or provident fund rates")
	}
	salary, gross := satang(in.Salary), satang(in.Salary+in.Overtime+in.Bonus+in.TaxableBenefits)
	sso := ThaiSocialSecurity(in.SocialSecurityWage, in.Insured)
	futureSSO := ThaiSocialSecurity(in.Salary, in.Insured)
	provident := roundRatio(salary, int64(math.Round(in.ProvidentEmployeeRate*10000)), 10000)
	employerProvident := roundRatio(salary, int64(math.Round(in.ProvidentEmployerRate*10000)), 10000)
	remaining := int64(12 - in.Month)
	annual := satang(in.YTDGross) + gross + salary*remaining
	expense := min(roundRatio(annual, 50, 100), int64(100000*100))
	allowance := int64(60000*100) + satang(in.YTDSSO) + sso + futureSSO*remaining + int64(in.Children30*30000+in.Children60*60000+in.Parents*30000)*100 + satang(in.CertifiedOtherAllowance)
	if in.SpouseNoIncome {
		allowance += 60000 * 100
	}
	annualProvident := satang(in.YTDProvident) + provident*(remaining+1)
	allowance += min(annualProvident, min(roundRatio(annual, 15, 100), int64(500000*100)))
	taxable := max(int64(0), annual-expense-allowance)
	tax := ThaiAnnualTax(taxable)
	withheld := roundRatio(max(int64(0), tax-satang(in.YTDWithheld)), 1, remaining+1)
	net := gross + satang(in.Reimbursements) - sso - provident - withheld - satang(in.OtherDeductions)
	if net < 0 {
		return out, errors.New("deductions exceed current pay")
	}
	out.Gross = baht(gross)
	out.Overtime = in.Overtime
	out.EmployeeSSO = baht(sso)
	out.EmployerSSO = baht(sso)
	out.ProvidentEmployee = baht(provident)
	out.ProvidentEmployer = baht(employerProvident)
	out.ProjectedAnnualGross = baht(annual)
	out.ExpenseDeduction = baht(expense)
	out.Allowances = baht(allowance)
	out.TaxableAnnualIncome = baht(taxable)
	out.AnnualTax = baht(tax)
	out.Withholding = baht(withheld)
	out.NetPay = baht(net)
	out.EmployerCost = baht(gross + sso + employerProvident)
	return out, nil
}
