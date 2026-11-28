package store

import "context"

// clampRating keeps the stored 0-10 scale in range. Zero means unrated and is
// never written to game_ratings.
func clampRating(rating float64) float64 {
	if rating < 0 {
		return 0
	}
	if rating > 10 {
		return 10
	}
	return rating
}

// SetRating stores the one rating a user has for a game. It does not create a
// log or touch the user's shelf or play count. A zero rating clears the row.
func (s *Store) SetRating(ctx context.Context, userID, gameID int64, rating float64) error {
	rating = clampRating(rating)
	now := Now()
	var err error
	if rating == 0 {
		_, err = s.DB.ExecContext(ctx, `DELETE FROM game_ratings WHERE user_id=? AND game_id=?`, userID, gameID)
	} else {
		_, err = s.DB.ExecContext(ctx, `INSERT INTO game_ratings(user_id,game_id,rating,created_at,updated_at)
			VALUES(?,?,?,?,?) ON CONFLICT(user_id,game_id) DO UPDATE SET
			rating=excluded.rating, updated_at=excluded.updated_at`, userID, gameID, rating, now, now)
	}
	if err != nil {
		return err
	}
	return s.RefreshGameStats(ctx, gameID)
}

// ClearRating explicitly removes a user's rating for a game.
func (s *Store) ClearRating(ctx context.Context, userID, gameID int64) error {
	return s.SetRating(ctx, userID, gameID, 0)
}

// RatingForUser returns the user's central rating for a game, or 0 when
// unrated. It never falls back to a log's historical rating.
func (s *Store) RatingForUser(ctx context.Context, userID, gameID int64) float64 {
	var rating float64
	s.DB.QueryRowContext(ctx, `SELECT rating FROM game_ratings WHERE user_id=? AND game_id=? AND rating>0`, userID, gameID).Scan(&rating)
	return rating
}
