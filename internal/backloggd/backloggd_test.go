package backloggd

import (
	"strings"
	"testing"
)

func TestParseBackloggdCSV(t *testing.T) {
	body := `ID,Game Name,Played,Currently Playing,In Backlog,In Wishlist,Liked,Rating,Mastered,Start Date,Finish Date,Total Hours,Total Minutes,Review,Review Contains Spoilers,Backloggd URL
1217,Shenmue,true,false,false,false,false,9,true,2024-08-04,2026-05-19,47,30,,false,https://backloggd.com/games/shenmue/
158980,Final Fantasy,false,true,true,false,false,0,false,2024-03-09,,0,0,,false,https://backloggd.com/games/final-fantasy--3/
`
	entries, err := Parse(strings.NewReader(body), "library.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	first := entries[0]
	if first.IGDBID != 1217 || first.Title != "Shenmue" || !first.Played || !first.Mastered {
		t.Fatalf("unexpected first entry: %+v", first)
	}
	if first.Rating != 9 || first.StartedAt != "2024-08-04" || first.FinishedAt != "2026-05-19" || first.Minutes != 47*60+30 {
		t.Fatalf("unexpected first entry values: %+v", first)
	}
	second := entries[1]
	if !second.Playing || !second.Backlog || second.Played || second.Slug != "final-fantasy--3" {
		t.Fatalf("unexpected second entry: %+v", second)
	}
}

func TestParseBackloggdJSONPlaythroughs(t *testing.T) {
	body := `[{
		"id": "1217",
		"name": "Shenmue",
		"game_log": {
			"status": "completed", "rating": 9, "is_play": true, "is_playing": false,
			"is_backlog": false, "is_wishlist": false, "game_liked": false,
			"total_hours": 0, "total_minutes": 0
		},
		"playthroughs": {
			"1": {
				"id": 1, "created_at": "May 14 2026", "updated_at": "May 19 2026",
				"review": "A quiet, patient journey.", "review_spoilers": true,
				"hours_played": 0, "mins_played": 0,
				"hours_finished": 47, "mins_finished": 30,
				"is_master": true, "is_replay": false,
				"start_date": "2024-08-04", "finish_date": "2026-05-19"
			}
		},
		"most_recent_playthrough_id": 1
	}]`
	entries, err := Parse(strings.NewReader(body), "library.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	e := entries[0]
	if e.Review != "A quiet, patient journey." || !e.Spoiler {
		t.Fatalf("review not parsed: %+v", e)
	}
	if !e.Mastered || e.Minutes != 47*60+30 || e.StartedAt != "2024-08-04" || e.FinishedAt != "2026-05-19" {
		t.Fatalf("playthrough not merged: %+v", e)
	}
}

func TestParseGenericCSV(t *testing.T) {
	entries, err := Parse(strings.NewReader("title,rating,review\nPortal,5,Still great.\n"), "old.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Rating != 10 || !entries[0].Played || entries[0].Review != "Still great." {
		t.Fatalf("unexpected generic entry: %+v", entries)
	}
}
