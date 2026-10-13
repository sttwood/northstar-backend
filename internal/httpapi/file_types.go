package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"northstar/backend/internal/domain"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func uploadType(category, name string, b []byte) (string, error) {
	pdf := bytes.HasPrefix(b, []byte("%PDF-"))
	detected := http.DetectContentType(b)
	if category == "community-image" {
		kind, err := profileImageType(b)
		if err != nil {
			return "", errors.New("community images require a valid PNG or JPEG up to 2 MB and 4096 pixels")
		}
		return kind, nil
	}
	if category == "avatar" {
		return profileImageType(b)
	}
	if category != "project-resource" {
		if pdf {
			return "application/pdf", nil
		}
		if category == "attachment" && (detected == "image/png" || detected == "image/jpeg" || strings.HasPrefix(detected, "text/plain")) {
			return detected, nil
		}
		return "", errors.New("payslips, reference files and official documents must be PDFs; attachments accept PDF, PNG, JPEG or text")
	}
	ext := strings.ToLower(filepath.Ext(name))
	if pdf && ext == ".pdf" {
		return "application/pdf", nil
	}
	if (ext == ".png" && detected == "image/png") || (domain.Contains([]string{".jpg", ".jpeg"}, ext) && detected == "image/jpeg") || (ext == ".webp" && detected == "image/webp") {
		return detected, nil
	}
	if domain.Contains([]string{".txt", ".md", ".csv", ".json", ".yaml", ".yml"}, ext) && utf8.Valid(b) && !bytes.Contains(b, []byte{0}) {
		if ext == ".json" {
			if !json.Valid(b) {
				return "", errors.New("invalid JSON resource")
			}
			return "application/json", nil
		}
		return "text/plain; charset=utf-8", nil
	}
	if ext == ".xlsx" || ext == ".docx" {
		archive, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil || len(archive.File) > 1000 {
			return "", errors.New("invalid Office resource")
		}
		required := "word/document.xml"
		mimeType := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		if ext == ".xlsx" {
			required = "xl/workbook.xml"
			mimeType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		}
		found, contentTypes := false, false
		var expanded uint64
		for _, file := range archive.File {
			expanded += file.UncompressedSize64
			if file.UncompressedSize64 > 50*1024*1024 || expanded > 50*1024*1024 || strings.Contains(strings.ToLower(file.Name), "vbaproject") {
				return "", errors.New("Office resource is too large or contains macros")
			}
			found = found || file.Name == required
			contentTypes = contentTypes || file.Name == "[Content_Types].xml"
		}
		if found && contentTypes {
			return mimeType, nil
		}
	}
	return "", errors.New("resources accept PDF, PNG, JPEG, WebP, TXT, Markdown, CSV, JSON, YAML, XLSX or DOCX")
}

func authorizeUpload(s *domain.State, u domain.Employee, f domain.File) error {
	if f.Category == "community-image" {
		if f.EmployeeID != u.ID || f.RecordID != "" || f.ProjectID != "" || f.Environment != "" || f.ResourceKind != "" {
			return fail(400, "community images cannot contain request or project metadata")
		}
		if e := s.Employee(u.ID); e == nil || !domain.Active(*e) {
			return fail(403, "active community membership required")
		}
		return nil
	}
	if f.Category == "payslip" && u.Role != "owner" {
		return fail(403, "only the owner may manage payslips")
	}
	employee := s.Employee(f.EmployeeID)
	if employee == nil || !domain.Active(*employee) {
		return fail(400, "active employee not found")
	}
	if f.Category == "avatar" {
		if f.EmployeeID != u.ID || f.ProjectID != "" || f.RecordID != "" || f.Environment != "" || f.ResourceKind != "" || f.Description != "" {
			return fail(400, "profile photos cannot contain request or project metadata")
		}
		return nil
	}
	if f.Category == "project-resource" {
		if f.Environment != "" {
			if !domain.Contains([]string{"dev", "sit", "uat", "prod"}, f.Environment) {
				return fail(400, "invalid project environment")
			}
			if !domain.Manager(u) {
				return fail(403, "only the owner may upload environment documents")
			}
		}
		if f.ProjectID == "" || !domain.ProjectAccess(s, u, f.ProjectID) {
			return fail(403, "project access required")
		}
		for _, p := range s.Records {
			if p.ID == f.ProjectID && p.Kind == "project" && p.Status != "active" {
				return fail(400, "restore the project before uploading resources")
			}
		}
		if f.RecordID != "" {
			return fail(400, "project resources cannot attach to a personal request")
		}
		if !domain.Contains([]string{"image", "api-spec", "data-dictionary", "document", "file"}, f.ResourceKind) {
			return fail(400, "choose a valid resource type")
		}
		return nil
	}
	if f.ProjectID != "" || f.ResourceKind != "" || f.Description != "" || f.Environment != "" {
		return fail(400, "project metadata requires a project resource")
	}
	if f.Category == "payslip-reference" {
		if u.Role != "owner" {
			return fail(403, "only the owner may manage payslip references")
		}
		for _, record := range s.Records {
			if record.ID == f.RecordID && record.Kind == "template" {
				return nil
			}
		}
		return fail(400, "choose a payslip template for the reference file")
	}
	if (f.Category != "attachment" || f.EmployeeID != u.ID) && !domain.Manager(u) {
		return fail(403, "only managers may upload payroll or official documents")
	}
	if f.RecordID != "" {
		for _, record := range s.Records {
			if record.ID == f.RecordID && record.EmployeeID == f.EmployeeID && domain.CanRead(s, u, record) && domain.Contains([]string{"leave", "overtime", "document", "request", "ticket", "payroll"}, record.Kind) {
				return nil
			}
		}
		return fail(403, "attached request must belong to the selected employee")
	}
	return nil
}
