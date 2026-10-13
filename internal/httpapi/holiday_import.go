package httpapi

import (
	"net/http"
	"northstar/backend/internal/domain"
)

func (a *API) holidayImport(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "only the owner may import holidays")
	}
	var input domain.HolidayCalendarImport
	if err := decode(w, r, &input); err != nil {
		return err
	}
	var result domain.HolidayImportResult
	if r.PathValue("mode") == "preview" {
		s, err := a.store.Read(r.Context())
		if err != nil {
			return err
		}
		result, err = domain.PreviewHolidayImport(&s, input)
		if err != nil {
			return fail(400, err.Error())
		}
	} else {
		err := a.store.Update(r.Context(), func(s *domain.State) error {
			if err := communityActor(s, u); err != nil {
				return err
			}
			if s.Employee(u.ID).Role != "owner" {
				return fail(403, "only the owner may import holidays")
			}
			var err error
			result, err = domain.PreviewHolidayImport(s, input)
			if err != nil {
				return fail(400, err.Error())
			}
			for _, row := range result.Rows {
				if row.Action != "create" {
					continue
				}
				record := domain.ImportedHolidayRecord(row, u, id(), stamp())
				if err = domain.Validate(s, u, &record, nil); err != nil {
					return fail(400, err.Error())
				}
				s.Records = append(s.Records, record)
				audit(s, u, "imported company holiday", record.ID)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	respond(w, 200, result)
	return nil
}
