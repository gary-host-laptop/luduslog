package store

import (
	"context"
	"database/sql"
	"fmt"

	"luduslog/internal/models"
)

const maxFavorites = 5

// FavoriteGames returns explicitly picked favourite games in display order.
func (s *Store) FavoriteGames(ctx context.Context, userID int64, limit int) []models.Game {
	if limit <= 0 || limit > maxFavorites {
		limit = maxFavorites
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+gameCols+` FROM favorite_games fg JOIN games g ON g.id=fg.game_id
		WHERE fg.user_id=? ORDER BY fg.position LIMIT ?`, userID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []models.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return out
		}
		out = append(out, *g)
	}
	return out
}

// AddFavoriteGame appends a game to the user's favourites.
func (s *Store) AddFavoriteGame(ctx context.Context, userID, gameID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM favorite_games WHERE user_id=? AND game_id=?`, userID, gameID).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM favorite_games WHERE user_id=?`, userID).Scan(&count); err != nil {
		return err
	}
	if count >= maxFavorites {
		return ErrFavoriteLimit
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM games WHERE id=?`, gameID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO favorite_games(user_id,game_id,position,created_at) VALUES(?,?,?,?)`,
		userID, gameID, count+1, Now()); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveFavoriteGame deletes a pick and compacts the remaining positions.
func (s *Store) RemoveFavoriteGame(ctx context.Context, userID, gameID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM favorite_games WHERE user_id=? AND game_id=?`, userID, gameID); err != nil {
		return err
	}
	if err := compactFavorites(ctx, tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// ReorderFavoriteGames rewrites positions transactionally. gameIDs must match
// the current favourite set exactly.
func (s *Store) ReorderFavoriteGames(ctx context.Context, userID int64, gameIDs []int64) error {
	current, err := favoriteIDs(ctx, s.DB, userID)
	if err != nil {
		return err
	}
	if len(gameIDs) != len(current) {
		return ErrInvalidOrder
	}
	want := make(map[int64]bool, len(current))
	for _, id := range current {
		want[id] = true
	}
	seen := make(map[int64]bool, len(gameIDs))
	for _, id := range gameIDs {
		if !want[id] || seen[id] {
			return ErrInvalidOrder
		}
		seen[id] = true
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM favorite_games WHERE user_id=?`, userID); err != nil {
		return err
	}
	for i, id := range gameIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO favorite_games(user_id,game_id,position,created_at) VALUES(?,?,?,?)`,
			userID, id, i+1, Now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type favoriteQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func favoriteIDs(ctx context.Context, q favoriteQuerier, userID int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT game_id FROM favorite_games WHERE user_id=? ORDER BY position`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func compactFavorites(ctx context.Context, tx *sql.Tx, userID int64) error {
	ids, err := favoriteIDs(ctx, tx, userID)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE favorite_games SET position=? WHERE user_id=? AND game_id=?`, i+1, userID, id); err != nil {
			return fmt.Errorf("compact favourite %d: %w", id, err)
		}
	}
	return nil
}
