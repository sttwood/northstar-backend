package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/store"
	"strconv"
	"strings"
	"time"
)

func (a *API) communityRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/holidays/import", a.auth(endpoint(a.holidayImport)))
	mux.Handle("POST /api/holidays/import/{mode}", a.auth(endpoint(func(w http.ResponseWriter, r *http.Request) error {
		if r.PathValue("mode") != "preview" {
			return fail(404, "import mode not found")
		}
		return a.holidayImport(w, r)
	})))
	a.shopRoutes(mux)
	mux.Handle("GET /api/community/badges", a.auth(endpoint(a.communityBadges)))
	routes := map[string]func(http.ResponseWriter, *http.Request) error{"GET /api/community": a.communityList, "GET /api/community/summary": a.communitySummary, "POST /api/community/posts": a.communityPublish, "GET /api/community/posts/{id}": a.communityPost, "PATCH /api/community/posts/{id}": a.communityEditPost, "GET /api/community/posts/{id}/answers": a.communityAnswers, "POST /api/community/posts/{id}/answers": a.communityAnswer, "PATCH /api/community/answers/{id}": a.communityEditAnswer, "POST /api/community/posts/{id}/resolve": a.communityResolve}
	for path, fn := range routes {
		mux.Handle(path, a.auth(endpoint(fn)))
	}
}
func (a *API) communityBadges(w http.ResponseWriter, r *http.Request) error {
	ids := strings.Split(r.URL.Query().Get("employeeIds"), ",")
	if len(ids) > 100 {
		return fail(400, "choose up to 100 community authors")
	}
	for _, id := range ids {
		if id == "" || len(id) > 100 {
			return fail(400, "invalid community author")
		}
	}
	badges, err := a.communityRepository().CommunityBadges(r.Context(), current(r), ids, time.Now())
	if err != nil {
		return err
	}
	respond(w, 200, badges)
	return nil
}
func (a *API) communityRepository() store.CommunityRepository {
	return a.store.(store.CommunityRepository)
}
func communityLimit(r *http.Request) int {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		return 20
	}
	return min(limit, 50)
}
func (a *API) communityList(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	kind, status := q.Get("type"), q.Get("status")
	if kind != "" && !domain.Contains([]string{"help", "knowledge"}, kind) || status != "" && !domain.Contains([]string{"open", "resolved"}, status) || len(q.Get("search")) > 500 {
		return fail(400, "invalid community filter")
	}
	page, err := a.communityRepository().CommunityPage(r.Context(), current(r), store.CommunityFilter{Type: kind, Status: status, Search: q.Get("search"), Cursor: q.Get("cursor"), Limit: communityLimit(r)})
	if err != nil {
		return communityPageError(err)
	}
	respond(w, 200, page)
	return nil
}
func communityPageError(err error) error {
	if errors.Is(err, store.ErrCommunityPostNotFound) {
		return fail(404, err.Error())
	}
	if err.Error() == "invalid community cursor" {
		return fail(400, err.Error())
	}
	return err
}
func (a *API) communitySummary(w http.ResponseWriter, r *http.Request) error {
	summary, err := a.communityRepository().CommunitySummary(r.Context(), current(r), time.Now())
	if err != nil {
		return err
	}
	respond(w, 200, summary)
	return nil
}
func (a *API) communityPost(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	post, err := a.communityRepository().CommunityPost(r.Context(), u, r.PathValue("id"))
	if err != nil {
		return communityPageError(err)
	}
	var solution *domain.Record
	authors, err := a.communityRepository().CommunityAuthors(r.Context(), u, []string{post.EmployeeID})
	if err != nil {
		return err
	}
	if accepted := domain.Text(post, "acceptedAnswerId"); accepted != "" {
		page, err := a.communityRepository().CommunityPage(r.Context(), u, store.CommunityFilter{PostID: post.ID, AnswerID: accepted, Limit: 1})
		if err != nil {
			return err
		}
		if len(page.Items) > 0 {
			solution = &page.Items[0]
			authors = append(authors, page.Authors...)
		}
	}
	respond(w, 200, map[string]any{"post": post, "solution": solution, "authors": authors})
	return nil
}
func (a *API) communityAnswers(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if _, err := a.communityRepository().CommunityPost(r.Context(), u, r.PathValue("id")); err != nil {
		return communityPageError(err)
	}
	page, err := a.communityRepository().CommunityPage(r.Context(), u, store.CommunityFilter{PostID: r.PathValue("id"), Cursor: r.URL.Query().Get("cursor"), Limit: communityLimit(r)})
	if err != nil {
		return communityPageError(err)
	}
	respond(w, 200, page)
	return nil
}

type communityBody struct {
	Title   string          `json:"title"`
	Body    string          `json:"body"`
	BodyDoc json.RawMessage `json:"bodyDoc"`
	Type    string          `json:"type"`
	Tags    []string        `json:"tags"`
}

func communityRecord(s *domain.State, id, kind string) *domain.Record {
	for i := range s.Records {
		if s.Records[i].ID == id && s.Records[i].Kind == kind {
			return &s.Records[i]
		}
	}
	return nil
}
func communityActor(s *domain.State, u domain.Employee) error {
	e := s.Employee(u.ID)
	if e == nil || !domain.Active(*e) || domain.CompanyID(e.CompanyID) != domain.CompanyID(u.CompanyID) {
		return fail(403, "active community membership required")
	}
	return nil
}
func (a *API) communityPublish(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	var body communityBody
	if err := decode(w, r, &body); err != nil {
		return err
	}
	text, doc, richErr := domain.CommunityRichText(body.BodyDoc, body.Body)
	if richErr != nil {
		return fail(400, richErr.Error())
	}
	body.Body = text
	if err := domain.ValidateCommunityContent(body.Title, body.Body, body.Type, body.Tags); err != nil {
		return fail(400, err.Error())
	}
	now := time.Now().UTC()
	post := domain.Record{ID: id(), CompanyID: domain.CompanyID(u.CompanyID), EmployeeID: u.ID, Kind: "community-post", Title: strings.TrimSpace(body.Title), Status: "open", CreatedAt: now.Format("2006-01-02T15:04:05.000000000Z"), Data: map[string]any{"type": body.Type, "body": strings.TrimSpace(body.Body), "tags": body.Tags, "answerCount": 0}}
	if doc != nil {
		post.Data["bodyDoc"] = doc
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := communityActor(s, u); err != nil {
			return err
		}
		s.Records = append(s.Records, post)
		if err := bindCommunityImages(s, u, &post); err != nil {
			return err
		}
		domain.AddCommunityActivity(s, u, now, id())
		audit(s, u, "published community post", post.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 201, post)
	return nil
}
func (a *API) communityAnswer(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	var body struct {
		Body    string          `json:"body"`
		BodyDoc json.RawMessage `json:"bodyDoc"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	text, doc, richErr := domain.CommunityRichText(body.BodyDoc, body.Body)
	if richErr != nil {
		return fail(400, richErr.Error())
	}
	body.Body = text
	if err := domain.ValidateCommunityContent("", body.Body, "answer", nil); err != nil {
		return fail(400, err.Error())
	}
	now := time.Now().UTC()
	answer := domain.Record{ID: id(), CompanyID: domain.CompanyID(u.CompanyID), EmployeeID: u.ID, Kind: "community-answer", Title: "Community answer", Status: "active", CreatedAt: now.Format("2006-01-02T15:04:05.000000000Z"), Data: map[string]any{"postId": r.PathValue("id"), "body": strings.TrimSpace(body.Body)}}
	if doc != nil {
		answer.Data["bodyDoc"] = doc
	}
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := communityActor(s, u); err != nil {
			return err
		}
		post := communityRecord(s, r.PathValue("id"), "community-post")
		if post == nil {
			return fail(404, "community post not found")
		}
		if post.Status == "resolved" {
			return fail(409, "this question already has an accepted solution")
		}
		post.Data["answerCount"] = domain.Number(*post, "answerCount") + 1
		s.Records = append(s.Records, answer)
		if err := bindCommunityImages(s, u, &answer); err != nil {
			return err
		}
		if post.EmployeeID != u.ID {
			communityNotice(s, post.EmployeeID, "New community reply", post.ID)
		}
		domain.AddCommunityActivity(s, u, now, id())
		audit(s, u, "answered community post", post.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 201, answer)
	return nil
}
func (a *API) communityResolve(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	var body struct {
		AnswerID string `json:"answerId"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	var result domain.Record
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := communityActor(s, u); err != nil {
			return err
		}
		post := communityRecord(s, r.PathValue("id"), "community-post")
		if post == nil {
			return fail(404, "community post not found")
		}
		if post.EmployeeID != u.ID && !domain.Manager(*s.Employee(u.ID)) {
			return fail(403, "only the post author or an owner may accept a solution")
		}
		if domain.Text(*post, "type") != "help" {
			return fail(400, "only help questions can be resolved")
		}
		if post.Status == "resolved" {
			return fail(409, "this question already has an accepted solution")
		}
		answer := communityRecord(s, body.AnswerID, "community-answer")
		if answer == nil || domain.Text(*answer, "postId") != post.ID {
			return fail(400, "choose an answer from this question")
		}
		if answer.EmployeeID == u.ID || answer.EmployeeID == post.EmployeeID {
			return fail(400, "you cannot award points to yourself")
		}
		post.Status = "resolved"
		post.Data["acceptedAnswerId"] = answer.ID
		post.Data["acceptedBy"] = u.ID
		post.Data["acceptedAt"] = stamp()
		communityNotice(s, answer.EmployeeID, "Your solution was accepted", post.ID)
		result = *post
		audit(s, u, "accepted community solution", post.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 200, result)
	return nil
}
func (a *API) communityEditPost(w http.ResponseWriter, r *http.Request) error {
	return a.communityEdit(w, r, "community-post")
}
func (a *API) communityEditAnswer(w http.ResponseWriter, r *http.Request) error {
	return a.communityEdit(w, r, "community-answer")
}
func (a *API) communityEdit(w http.ResponseWriter, r *http.Request, kind string) error {
	u := current(r)
	var body communityBody
	if err := decode(w, r, &body); err != nil {
		return err
	}
	text, doc, richErr := domain.CommunityRichText(body.BodyDoc, body.Body)
	if richErr != nil {
		return fail(400, richErr.Error())
	}
	body.Body = text
	var result domain.Record
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := communityActor(s, u); err != nil {
			return err
		}
		record := communityRecord(s, r.PathValue("id"), kind)
		if record == nil {
			return fail(404, "community post not found")
		}
		if record.EmployeeID != u.ID {
			return fail(403, "you may edit only your own community content")
		}
		if kind == "community-post" {
			if record.Status == "resolved" {
				return fail(409, "accepted solutions are retained with their original question")
			}
			if body.Type != "" && body.Type != domain.Text(*record, "type") {
				return fail(400, "post type cannot be changed")
			}
			if err := domain.ValidateCommunityContent(body.Title, body.Body, domain.Text(*record, "type"), body.Tags); err != nil {
				return fail(400, err.Error())
			}
			record.Title = strings.TrimSpace(body.Title)
			record.Data["tags"] = body.Tags
		} else {
			parent := communityRecord(s, domain.Text(*record, "postId"), "community-post")
			if parent != nil && domain.Text(*parent, "acceptedAnswerId") == record.ID {
				return fail(409, "accepted answers cannot be edited")
			}
			if err := domain.ValidateCommunityContent("", body.Body, "answer", nil); err != nil {
				return fail(400, err.Error())
			}
		}
		record.Data["body"] = strings.TrimSpace(body.Body)
		delete(record.Data, "bodyDoc")
		if doc != nil {
			record.Data["bodyDoc"] = doc
		}
		record.Data["editedAt"] = stamp()
		if err := bindCommunityImages(s, u, record); err != nil {
			return err
		}
		result = *record
		audit(s, u, "edited community content", record.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 200, result)
	return nil
}
