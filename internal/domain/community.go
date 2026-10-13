package domain

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"
)

func CommunityKind(kind string) bool { return kind == "community-post" || kind == "community-answer" }

type CommunityPerson struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Team         string     `json:"team"`
	Avatar       string     `json:"avatar"`
	AvatarFileID string     `json:"avatarFileId,omitempty"`
	Decoration   Decoration `json:"decoration,omitempty"`
}

func CommunityAuthor(e Employee) CommunityPerson {
	return CommunityPerson{e.ID, e.Name, e.Team, e.Avatar, e.AvatarFileID, e.Decoration}
}

type CommunityPage struct {
	Items      []Record          `json:"items"`
	Authors    []CommunityPerson `json:"authors"`
	NextCursor string            `json:"nextCursor"`
}
type Achievement struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Goal        int    `json:"goal"`
	Progress    int    `json:"progress"`
	Unlocked    bool   `json:"unlocked"`
	Category    string `json:"category"`
}
type CommunityStats struct {
	Points        int           `json:"points"`
	Resolved      int           `json:"resolved"`
	Posts         int           `json:"posts"`
	Answers       int           `json:"answers"`
	Knowledge     int           `json:"knowledge"`
	CurrentStreak int           `json:"currentStreak"`
	BestStreak    int           `json:"bestStreak"`
	Achievements  []Achievement `json:"achievements"`
}
type CommunityLeader struct {
	Person   CommunityPerson `json:"person"`
	Resolved int             `json:"resolved"`
	Points   int             `json:"points"`
}
type CommunitySummary struct {
	Stats   CommunityStats    `json:"stats"`
	Leaders []CommunityLeader `json:"leaders"`
}

func ValidateCommunityContent(title, body, kind string, tags []string) error {
	if kind != "answer" && (!Contains([]string{"help", "knowledge"}, kind) || strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > 200) {
		return errors.New("choose a post type and a title of 1–200 characters")
	}
	if strings.TrimSpace(body) == "" || utf8.RuneCountInString(body) > 12000 {
		return errors.New("community content must be between 1 and 12000 characters")
	}
	if len(tags) > 5 {
		return errors.New("use at most five tags")
	}
	seen := map[string]bool{}
	for _, tag := range tags {
		key := strings.ToLower(strings.TrimSpace(tag))
		if key == "" || utf8.RuneCountInString(key) > 32 || seen[key] {
			return errors.New("tags must be unique and between 1 and 32 characters")
		}
		seen[key] = true
	}
	return nil
}
func CommunityDay(now time.Time) string {
	zone, _ := time.LoadLocation("Asia/Bangkok")
	return now.In(zone).Format("2006-01-02")
}
func AddCommunityActivity(s *State, u Employee, now time.Time, id string) {
	day := CommunityDay(now)
	for _, op := range s.Operations {
		if op.Kind == "community-activity" && op.EmployeeID == u.ID && CompanyID(op.CompanyID) == CompanyID(u.CompanyID) && op.Data["date"] == day {
			return
		}
	}
	s.Operations = append(s.Operations, Operation{ID: id, CompanyID: CompanyID(u.CompanyID), EmployeeID: u.ID, Kind: "community-activity", Status: "recorded", CreatedAt: now.UTC().Format(time.RFC3339Nano), Data: map[string]any{"date": day}})
}
func BuildCommunityStats(posts, answers, knowledge, resolved int, dates []string, now time.Time) CommunityStats {
	stats := CommunityStats{Points: resolved * 10, Resolved: resolved, Posts: posts, Answers: answers, Knowledge: knowledge, Achievements: []Achievement{}}
	today := CommunityDay(now)
	unique := map[string]bool{}
	for _, date := range dates {
		if _, err := time.Parse("2006-01-02", date); err == nil && date <= today {
			unique[date] = true
		}
	}
	days := []string{}
	for day := range unique {
		days = append(days, day)
	}
	sort.Strings(days)
	streak := 0
	previous := ""
	for _, day := range days {
		date, _ := time.Parse("2006-01-02", day)
		if previous == date.AddDate(0, 0, -1).Format("2006-01-02") {
			streak++
		} else {
			streak = 1
		}
		stats.BestStreak = max(stats.BestStreak, streak)
		previous = day
	}
	if len(days) > 0 {
		date, _ := time.Parse("2006-01-02", today)
		if previous == today || previous == date.AddDate(0, 0, -1).Format("2006-01-02") {
			stats.CurrentStreak = streak
		}
	}
	add := func(id, title, description, category string, goal, progress int) {
		stats.Achievements = append(stats.Achievements, Achievement{id, title, description, goal, min(goal, progress), progress >= goal, category})
	}
	add("first-post", "First voice", "Publish your first community post.", "posts", 1, posts)
	add("first-answer", "Helping hand", "Write your first answer.", "answers", 1, answers)
	for _, entry := range []struct {
		goal  int
		title string
	}{{1, "Problem solver"}, {3, "Team helper"}, {5, "Junior champion"}, {10, "Trusted guide"}, {25, "Community mentor"}, {50, "Knowledge hero"}} {
		add("resolve-"+strconv.Itoa(entry.goal), entry.title, "Help resolve {goal} questions.", "resolved", entry.goal, resolved)
	}
	for _, entry := range []struct {
		goal  int
		title string
	}{{1, "Knowledge sharer"}, {3, "Knowledge builder"}, {10, "Team librarian"}} {
		add("knowledge-"+strconv.Itoa(entry.goal), entry.title, "Publish {goal} knowledge posts.", "knowledge", entry.goal, knowledge)
	}
	for _, entry := range []struct {
		goal  int
		title string
	}{{5, "Conversation starter"}, {25, "Always helpful"}} {
		add("answers-"+strconv.Itoa(entry.goal), entry.title, "Write {goal} answers.", "answers", entry.goal, answers)
	}
	for _, entry := range []struct {
		goal  int
		title string
	}{{5, "Five-day spark"}, {10, "Steady contributor"}, {30, "Community regular"}} {
		add("streak-"+strconv.Itoa(entry.goal), entry.title, "Contribute on {goal} consecutive days.", "streak", entry.goal, stats.BestStreak)
	}
	return stats
}
