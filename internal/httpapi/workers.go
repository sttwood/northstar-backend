package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/integrations"
	"strconv"
	"time"
)

func (a *API) RunWorkers(ctx context.Context) {
	jobs := time.NewTicker(5 * time.Second)
	defer jobs.Stop()
	retention := time.NewTicker(24 * time.Hour)
	defer retention.Stop()
	runRetention := func() {
		companies, err := a.store.(interface {
			Companies(context.Context) ([]domain.Company, error)
		}).Companies(ctx)
		if err != nil {
			return
		}
		for _, c := range companies {
			if _, err = a.applyRetention(ctx, c.ID); err != nil {
				slog.Error("retention failed", "company", c.ID)
			}
		}
	}
	runRetention()
	for {
		if err := a.ProcessJobs(ctx); err != nil && ctx.Err() == nil {
			slog.Error("background queue failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-jobs.C:
		case <-retention.C:
			runRetention()
		}
	}
}
func (a *API) ProcessJobs(ctx context.Context) error {
	companies, err := a.store.(interface {
		Companies(context.Context) ([]domain.Company, error)
	}).Companies(ctx)
	if err != nil {
		return err
	}
	for _, company := range companies {
		tenantCtx := domain.WithTenant(ctx, company.ID)
		for range 20 {
			var job *domain.Operation
			err = a.store.Update(tenantCtx, func(s *domain.State) error {
				now := time.Now()
				for i := range s.Operations {
					op := &s.Operations[i]
					if !domain.Contains([]string{"email", "drive-export", "drive-import", "blob-delete", "drive-delete"}, op.Kind) || !domain.Contains([]string{"queued", "processing"}, op.Status) {
						continue
					}
					next, e := time.Parse(time.RFC3339Nano, op.NextAt)
					if e == nil && next.After(now) {
						continue
					}
					op.Status = "processing"
					op.Attempts++
					op.NextAt = now.Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)
					copy := *op
					job = &copy
					return nil
				}
				return nil
			})
			if err != nil {
				return err
			}
			if job == nil {
				break
			}
			jobCtx, cancel := context.WithTimeout(tenantCtx, 90*time.Second)
			result, jobErr := a.executeJob(jobCtx, *job)
			cancel()
			err = a.store.Update(tenantCtx, func(s *domain.State) error {
				for i := range s.Operations {
					op := &s.Operations[i]
					if op.ID != job.ID || op.Status != "processing" || op.Attempts != job.Attempts {
						continue
					}
					if jobErr == nil {
						op.Status = "sent"
						op.NextAt = ""
						delete(op.Data, "body")
						if result != "" {
							op.Data["result"] = result
						}
					} else if op.Attempts >= 8 {
						op.Status = "failed"
					} else {
						op.Status = "queued"
						op.NextAt = time.Now().Add(time.Duration(1<<min(op.Attempts, 10)) * time.Minute).UTC().Format(time.RFC3339Nano)
					}
					return nil
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}
func (a *API) executeJob(ctx context.Context, job domain.Operation) (string, error) {
	switch job.Kind {
	case "email":
		if a.cfg.Mailer == nil {
			return "", errors.New("email provider unavailable")
		}
		body, err := a.open(opText(job, "body"))
		if err != nil {
			return "", err
		}
		return a.cfg.MailMode, a.cfg.Mailer.Send(ctx, opText(job, "to"), opText(job, "subject"), body)
	case "blob-delete":
		return "", a.cfg.Blobs.Delete(ctx, opText(job, "key"))
	case "drive-delete":
		if a.cfg.Drive == nil {
			return "", errors.New("Drive unavailable")
		}
		return "", a.cfg.Drive.Delete(ctx, opText(job, "driveId"))
	case "drive-export":
		if a.cfg.Drive == nil {
			return "", errors.New("Drive unavailable")
		}
		s, err := a.store.Read(ctx)
		if err != nil {
			return "", err
		}
		var file *domain.File
		for _, v := range s.Files {
			if v.ID == opText(job, "fileId") {
				copy := v
				file = &copy
				break
			}
		}
		if file == nil {
			return "", nil
		}
		c := s.Company(job.CompanyID)
		if c == nil || c.DriveFolderID != opText(job, "folder") {
			return "", errors.New("Drive folder changed")
		}
		b, err := a.cfg.Blobs.Get(ctx, fileKey(*file))
		if err != nil {
			return "", err
		}
		if a.cfg.Scanner == nil {
			return "", errors.New("scanner required for Drive sync")
		}
		if err = a.cfg.Scanner.Scan(ctx, b); err != nil {
			return "", err
		}
		remote, err := a.cfg.Drive.Export(ctx, c.DriveFolderID, c.ID, file.ID, file.EmployeeID, file.Name, file.Category, file.Month, file.Year, b)
		if err != nil {
			return "", err
		}
		err = a.store.Update(ctx, func(s *domain.State) error {
			stillExists := false
			for _, v := range s.Files {
				if v.ID == file.ID {
					stillExists = true
				}
			}
			if !stillExists {
				queueJob(s, c.ID, "drive-delete", map[string]any{"driveId": remote})
				return nil
			}
			for _, v := range s.Operations {
				if v.Kind == "drive-mapping" && opText(v, "fileId") == file.ID {
					return nil
				}
			}
			s.Operations = append(s.Operations, domain.Operation{ID: id(), CompanyID: c.ID, Kind: "drive-mapping", Status: "active", CreatedAt: stamp(), Data: map[string]any{"fileId": file.ID, "driveId": remote, "sha256": integrations.Digest(b)}})
			return nil
		})
		return remote, err
	case "drive-import":
		return "", a.importDrive(ctx, job)
	}
	return "", errors.New("unknown background job")
}
func (a *API) importDrive(ctx context.Context, job domain.Operation) error {
	if a.cfg.Drive == nil || a.cfg.Scanner == nil {
		return errors.New("Drive and file scanner must be configured")
	}
	s, err := a.store.Read(ctx)
	if err != nil {
		return err
	}
	c := s.Company(job.CompanyID)
	if c == nil || c.DriveFolderID != opText(job, "folder") {
		return errors.New("Drive folder changed")
	}
	files, next, err := a.cfg.Drive.List(ctx, c.DriveFolderID, c.ID, opText(job, "cursor"))
	if err != nil {
		return err
	}
	for _, remote := range files {
		if remote.MimeType != "application/pdf" || remote.Properties["northstarCompanyId"] != c.ID {
			continue
		}
		employee := s.Employee(remote.Properties["employeeId"])
		if employee == nil || !domain.Active(*employee) {
			continue
		}
		category := remote.Properties["category"]
		if category != "payslip" && category != "document" {
			continue
		}
		month, _ := strconv.Atoi(remote.Properties["month"])
		year, _ := strconv.Atoi(remote.Properties["year"])
		if category == "payslip" && (month < 1 || month > 12 || year < 2000 || year > 2200) {
			continue
		}
		b, err := a.cfg.Drive.Download(ctx, remote.ID)
		if err != nil {
			return err
		}
		if len(b) == 0 || !bytes.HasPrefix(b, []byte("%PDF-")) {
			continue
		}
		digest := integrations.Digest(b)
		known := false
		for _, op := range s.Operations {
			if op.Kind == "drive-delete" && opText(op, "driveId") == remote.ID {
				known = true
			}
			if op.Kind == "drive-mapping" && opText(op, "driveId") == remote.ID && opText(op, "sha256") == digest {
				known = true
			}
		}
		if known {
			continue
		}
		if err = a.cfg.Scanner.Scan(ctx, b); err != nil {
			return err
		}
		file := domain.File{ID: id(), CompanyID: c.ID, EmployeeID: employee.ID, Category: category, Month: month, Year: year, Name: remote.Name, Size: int64(len(b)), CreatedAt: stamp(), ScanStatus: "clean"}
		file.StorageKey = c.ID + "/" + file.ID
		if err = a.cfg.Blobs.Put(ctx, file.StorageKey, b); err != nil {
			return err
		}
		err = a.store.Update(ctx, func(s *domain.State) error {
			current := s.Employee(employee.ID)
			if current == nil || !domain.Active(*current) {
				queueJob(s, c.ID, "blob-delete", map[string]any{"key": file.StorageKey})
				return nil
			}
			for _, op := range s.Operations {
				if op.Kind == "drive-delete" && opText(op, "driveId") == remote.ID {
					queueJob(s, c.ID, "blob-delete", map[string]any{"key": file.StorageKey})
					return nil
				}
				if op.Kind == "drive-mapping" && opText(op, "driveId") == remote.ID && opText(op, "sha256") == digest {
					queueJob(s, c.ID, "blob-delete", map[string]any{"key": file.StorageKey})
					return nil
				}
			}
			s.Files = append(s.Files, file)
			s.Operations = append(s.Operations, domain.Operation{ID: id(), CompanyID: c.ID, EmployeeID: employee.ID, Kind: "drive-mapping", Status: "active", CreatedAt: stamp(), Data: map[string]any{"fileId": file.ID, "driveId": remote.ID, "sha256": digest}})
			audit(s, domain.Employee{ID: "system", CompanyID: c.ID}, "imported Drive file", file.ID)
			return nil
		})
		if err != nil {
			_ = a.cfg.Blobs.Delete(ctx, file.StorageKey)
			return err
		}
	}
	if next != "" {
		return a.store.Update(ctx, func(s *domain.State) error {
			queueJob(s, c.ID, "drive-import", map[string]any{"folder": c.DriveFolderID, "cursor": next})
			return nil
		})
	}
	return nil
}
func (a *API) applyRetention(ctx context.Context, company string) (int, error) {
	count := 0
	ctx = domain.WithTenant(ctx, company)
	err := a.store.Update(ctx, func(s *domain.State) error {
		c := s.Company(company)
		if c == nil {
			return nil
		}
		now := time.Now()
		purge := map[string]bool{}
		purgedEmails := map[string]bool{}
		for i := range s.Employees {
			e := &s.Employees[i]
			if e.Status != "offboarded" || e.RetainUntil == "" {
				continue
			}
			until, err := time.Parse(time.RFC3339Nano, e.RetainUntil)
			if err != nil || until.After(now) {
				continue
			}
			hold := false
			for _, op := range s.Operations {
				if op.Kind == "offboarding" && op.EmployeeID == e.ID {
					value, _ := op.Data["legalHold"].(bool)
					hold = hold || value
				}
			}
			if hold {
				continue
			}
			purge[e.ID] = true
			purgedEmails[e.Email] = true
			count++
			e.Name = "Former employee"
			e.Email = e.ID + "@removed.invalid"
			e.Phone = ""
			e.Location = ""
			e.Salary = 0
			e.PasswordHash = ""
			e.Status = "anonymized"
			e.RetainUntil = ""
			e.Avatar = ""
			e.AvatarFileID = ""
			e.Decoration = domain.Decoration{}
			e.Title = ""
			e.Team = ""
			e.StartDate = ""
			e.ManagerID = ""
			e.LeaveAllowance = 0
		}
		records := []domain.Record{}
		for _, record := range s.Records {
			if purge[record.EmployeeID] && domain.Contains([]string{"leave", "overtime", "document", "request", "ticket", "comment", "onboarding", "payroll"}, record.Kind) {
				continue
			}
			records = append(records, record)
		}
		s.Records = records
		files := []domain.File{}
		for _, file := range s.Files {
			created, parseErr := time.Parse(time.RFC3339Nano, file.CreatedAt)
			if file.Category == "community-image" && file.RecordID == "" && parseErr == nil && now.Sub(created) > 24*time.Hour {
				queueFileDeletion(s, file)
				continue
			}
			if purge[file.EmployeeID] && file.Category != "project-resource" && !(file.Category == "community-image" && file.RecordID != "") {
				queueFileDeletion(s, file)
			} else {
				files = append(files, file)
			}
		}
		s.Files = files
		accounts := []domain.AccountSecurity{}
		for _, v := range s.Security {
			if !purge[v.EmployeeID] {
				accounts = append(accounts, v)
			}
		}
		s.Security = accounts
		ops := []domain.Operation{}
		for _, op := range s.Operations {
			created, err := time.Parse(time.RFC3339Nano, op.CreatedAt)
			if purge[op.EmployeeID] && op.Kind == "shop-purchase" {
				delete(op.Data, "delivery")
				delete(op.Data, "key")
				ops = append(ops, op)
				continue
			}
			if (purge[op.EmployeeID] || (op.Kind == "email" && purgedEmails[opText(op, "to")]) || (op.Kind == "invitation" && purgedEmails[opText(op, "email")])) && op.Kind != "blob-delete" && op.Kind != "drive-delete" {
				continue
			}
			if err == nil && op.Kind == "notification" && now.Sub(created) > time.Duration(c.NotificationRetentionDays)*24*time.Hour {
				continue
			}
			if err == nil && domain.Contains([]string{"mfa-challenge", "oauth", "password-reset", "invitation"}, op.Kind) && now.Sub(created) > 30*24*time.Hour {
				continue
			}
			if err == nil && domain.Contains([]string{"email", "drive-export", "drive-import", "drive-delete", "blob-delete"}, op.Kind) && op.Status == "sent" && now.Sub(created) > 30*24*time.Hour {
				continue
			}
			ops = append(ops, op)
		}
		s.Operations = ops
		logs := []domain.Audit{}
		for _, log := range s.Audit {
			created, err := time.Parse(time.RFC3339Nano, log.CreatedAt)
			if err == nil && now.Sub(created) > time.Duration(c.AuditRetentionDays)*24*time.Hour {
				continue
			}
			logs = append(logs, log)
		}
		s.Audit = logs
		if count > 0 {
			audit(s, domain.Employee{ID: "system", CompanyID: company}, "applied retention", company)
		}
		return nil
	})
	return count, err
}
