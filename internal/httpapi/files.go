package httpapi

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"northstar/backend/internal/domain"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxUpload = 10 * 1024 * 1024

func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	if a.cfg.DisableUploads && !a.cfg.AllowImageUploads {
		if a.cfg.UploadPolicy == "images-only" {
			writeError(w, fail(503, "Image uploads are unavailable until private object storage is configured."))
			return
		}
		writeError(w, fail(503, "File uploads are unavailable until private object storage and scanning are configured."))
		return
	}
	u := current(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1024*1024)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeError(w, fail(400, "upload must be smaller than 10 MB"))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	source, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, fail(400, "choose a file"))
		return
	}
	defer source.Close()
	b, err := io.ReadAll(io.LimitReader(source, maxUpload+1))
	if err != nil || len(b) == 0 || len(b) > maxUpload {
		writeError(w, fail(400, "file must be between 1 byte and 10 MB"))
		return
	}
	category, employeeID := r.FormValue("category"), r.FormValue("employeeId")
	imageCategory := category == "avatar" || category == "community-image"
	if a.cfg.DisableUploads && !imageCategory {
		writeError(w, fail(503, "Document uploads require private object storage and antivirus scanning."))
		return
	}
	if !domain.Contains([]string{"payslip", "document", "attachment", "project-resource", "payslip-reference", "avatar", "community-image"}, category) {
		writeError(w, fail(400, "invalid file category"))
		return
	}
	if category == "project-resource" || category == "payslip-reference" || category == "avatar" || category == "community-image" {
		employeeID = u.ID
	}
	if category == "payslip-reference" && u.Role != "owner" {
		writeError(w, fail(403, "only the owner may manage payslip references"))
		return
	}
	if category != "project-resource" && category != "avatar" && category != "community-image" && (category != "attachment" || employeeID != u.ID) && !domain.Manager(u) {
		writeError(w, fail(403, "only managers may upload payroll or official documents"))
		return
	}
	month, _ := strconv.Atoi(r.FormValue("month"))
	year, _ := strconv.Atoi(r.FormValue("year"))
	if category == "payslip" && (month < 1 || month > 12 || year < 2000 || year > 2200) {
		writeError(w, fail(400, "valid payroll month and year required"))
		return
	}
	pdf := bytes.HasPrefix(b, []byte("%PDF-"))
	file := domain.File{ID: id(), CompanyID: domain.CompanyID(u.CompanyID), EmployeeID: employeeID, Name: filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/")), Category: category, Month: month, Year: year, Size: int64(len(b)), CreatedAt: stamp(), RecordID: r.FormValue("recordId"), ScanStatus: "not-scanned"}
	file.ProjectID = r.FormValue("projectId")
	file.ResourceKind = r.FormValue("resourceKind")
	file.Description = strings.TrimSpace(r.FormValue("description"))
	file.Environment = r.FormValue("environment")
	if utf8.RuneCountInString(file.Description) > 2000 || len(file.Name) > 500 {
		writeError(w, fail(400, "file description or name is too long"))
		return
	}
	file.MimeType, err = uploadType(category, file.Name, b)
	if err != nil {
		writeError(w, fail(400, err.Error()))
		return
	}
	state, err := a.store.Read(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	if err = authorizeUpload(&state, u, file); err != nil {
		writeError(w, err)
		return
	}
	file.StorageKey = file.CompanyID + "/" + file.ID
	if a.cfg.Scanner != nil {
		if err = a.cfg.Scanner.Scan(r.Context(), b); err != nil {
			writeError(w, fail(422, err.Error()))
			return
		}
		file.ScanStatus = "clean"
	} else if a.cfg.Production && !(a.cfg.AllowImageUploads && imageCategory) {
		writeError(w, fail(503, "file scanner unavailable"))
		return
	}
	if category == "avatar" {
		b, err = normalizeProfileImage(b)
		if err != nil {
			writeError(w, fail(400, err.Error()))
			return
		}
		file.Size = int64(len(b))
	}
	if category == "community-image" {
		b, err = normalizeCommunityImage(b)
		if err != nil {
			writeError(w, fail(400, err.Error()))
			return
		}
		file.Size = int64(len(b))
	}
	if a.cfg.Production && a.cfg.Scanner == nil && a.cfg.AllowImageUploads && imageCategory {
		file.ScanStatus = "image-normalized"
	}
	if err = a.cfg.Blobs.Put(r.Context(), file.StorageKey, b); err != nil {
		writeError(w, err)
		return
	}
	err = a.store.Update(r.Context(), func(s *domain.State) error {
		if err := authorizeUpload(s, u, file); err != nil {
			return err
		}
		if category == "avatar" {
			kept := []domain.File{}
			for _, old := range s.Files {
				if old.Category == "avatar" && old.EmployeeID == u.ID {
					queueFileDeletion(s, old)
				} else {
					kept = append(kept, old)
				}
			}
			s.Files = kept
			s.Employee(u.ID).AvatarFileID = file.ID
		}
		s.Files = append(s.Files, file)
		if pdf && domain.Contains([]string{"payslip", "document"}, category) {
			a.queueDrive(s, file)
		}
		audit(s, u, "uploaded "+category, file.ID)
		return nil
	})
	if err != nil {
		_ = a.cfg.Blobs.Delete(r.Context(), file.StorageKey)
		writeError(w, err)
		return
	}
	respond(w, 201, file)
}
func (a *API) download(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	s, err := a.store.Read(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	for _, file := range s.Files {
		if file.ID != r.PathValue("id") {
			continue
		}
		if !domain.CanReadFile(&s, u, file) {
			writeError(w, fail(403, "this file is private"))
			return
		}
		b, err := a.cfg.Blobs.Get(r.Context(), fileKey(file))
		if err != nil {
			writeError(w, fail(404, "file is unavailable"))
			return
		}
		disposition := "attachment"
		if file.Category == "avatar" || file.Category == "community-image" {
			disposition = "inline"
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": file.Name}))
		contentType := file.MimeType
		if contentType == "" {
			contentType = http.DetectContentType(b)
		}
		w.Header().Set("Content-Type", contentType)
		modified, _ := time.Parse(time.RFC3339Nano, file.CreatedAt)
		http.ServeContent(w, r, file.Name, modified, bytes.NewReader(b))
		return
	}
	writeError(w, fail(404, "file not found"))
}
func queueFileDeletion(s *domain.State, f domain.File) {
	queueJob(s, f.CompanyID, "blob-delete", map[string]any{"key": fileKey(f)})
	for _, op := range s.Operations {
		if op.Kind == "drive-mapping" && opText(op, "fileId") == f.ID {
			queueJob(s, f.CompanyID, "drive-delete", map[string]any{"driveId": opText(op, "driveId")})
		}
	}
	for i := range s.Operations {
		op := &s.Operations[i]
		if op.Kind == "drive-export" && opText(*op, "fileId") == f.ID && op.Status == "queued" {
			op.Status = "cancelled"
		}
	}
}
func (a *API) deleteFile(w http.ResponseWriter, r *http.Request) {
	u := current(r)
	target := r.PathValue("id")
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		for i, f := range s.Files {
			if f.ID != target {
				continue
			}
			if !domain.CanDeleteFile(s, u, f) {
				if f.Category == "community-image" && f.RecordID != "" {
					return fail(403, "published community images cannot be deleted")
				}
				return fail(403, "manager permission required")
			}
			queueFileDeletion(s, f)
			if f.Category == "avatar" {
				if e := s.Employee(f.EmployeeID); e != nil && e.AvatarFileID == f.ID {
					e.AvatarFileID = ""
				}
			}
			s.Files = append(s.Files[:i], s.Files[i+1:]...)
			audit(s, u, "deleted file", target)
			return nil
		}
		return fail(404, "file not found")
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 204, nil)
}
