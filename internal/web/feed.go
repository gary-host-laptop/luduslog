package web

import (
	"net/http"
	"time"

	"luduslog/internal/models"
)

// timelineStream is the shared read model for a paginated activity stream. The
// profile overview and the full feed render it through the same partial.
type timelineStream struct {
	Items        []models.TimelineItem
	Next         string
	Kind         string
	BasePath     string
	ShowCalendar bool
	CalendarView bool
	Calendar     *journalCalendar
}

func (s *Server) timelineStream(r *http.Request, viewerID int64, owners []int64, basePath string, withCalendar bool, limit int) timelineStream {
	stream := timelineStream{
		Kind:         timelineFilter(r.URL.Query().Get("kind")),
		BasePath:     basePath,
		ShowCalendar: withCalendar,
	}
	if withCalendar && r.URL.Query().Get("view") == "calendar" {
		stream.CalendarView = true
		at := time.Now()
		if month, err := time.Parse("2006-01", r.URL.Query().Get("month")); err == nil {
			at = month
		}
		days, _ := s.st.PlayCalendar(r.Context(), owners[0], 1000)
		stream.Calendar = buildJournalCalendar(days, at)
		return stream
	}
	stream.Items, stream.Next, _ = s.st.Timeline(r.Context(), viewerID, owners, stream.Kind, r.URL.Query().Get("cursor"), limit)
	return stream
}

type feedData struct {
	Page
	Stream timelineStream
}

func (s *Server) feedPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	owners := append([]int64{u.ID}, s.st.FollowingIDs(r.Context(), u.ID)...)
	s.render(w, r, "feed", feedData{
		Page:   s.page(r, "Feed"),
		Stream: s.timelineStream(r, u.ID, owners, "/feed", false, 30),
	})
}
