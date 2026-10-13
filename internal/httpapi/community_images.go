package httpapi

import (
	"encoding/json"
	"northstar/backend/internal/domain"
)

func bindCommunityImages(s *domain.State, u domain.Employee, r *domain.Record) error {
	if r.Data["bodyDoc"] == nil {
		return nil
	}
	b, _ := json.Marshal(r.Data["bodyDoc"])
	var doc domain.RichNode
	if err := json.Unmarshal(b, &doc); err != nil {
		return fail(400, "invalid community rich text")
	}
	var walk func(domain.RichNode) error
	walk = func(n domain.RichNode) error {
		if n.Type == "image" {
			fileID, _ := n.Attrs["fileId"].(string)
			found := false
			for i := range s.Files {
				f := &s.Files[i]
				if f.ID != fileID {
					continue
				}
				if f.Category != "community-image" || !domain.CanReadFile(s, u, *f) || (f.RecordID == "" && f.EmployeeID != u.ID) {
					return fail(403, "choose an uploaded community image")
				}
				if f.RecordID == "" {
					f.RecordID = r.ID
				}
				found = true
				break
			}
			if !found {
				return fail(400, "community image not found")
			}
		}
		for _, child := range n.Content {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(doc)
}
