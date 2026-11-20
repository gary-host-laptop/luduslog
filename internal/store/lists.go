package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"luduslog/internal/models"
)

const listCols = `l.id,l.user_id,l.slug,l.title,l.description,l.tags,l.privacy,l.ranked,l.progress,l.default_sort,
l.sort_dir,l.fade_played,l.display_mode,l.immersive,COALESCE(l.ap_id,''),l.created_at,l.updated_at`

func scanList(row scanner) (*models.List, error) {
	var l models.List
	var tags, created, updated string
	var ranked, progress, fade, immersive int
	err := row.Scan(&l.ID, &l.UserID, &l.Slug, &l.Title, &l.Description, &tags, &l.Privacy, &ranked, &progress,
		&l.DefaultSort, &l.SortDir, &fade, &l.DisplayMode, &immersive, &l.APID, &created, &updated)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(tags), &l.Tags)
	l.Ranked = ranked == 1
	l.Progress = progress == 1
	l.FadePlayed = fade == 1
	l.Immersive = immersive == 1
	l.CreatedAt = parseTime(created)
	l.UpdatedAt = parseTime(updated)
	return &l, nil
}

func (s *Store) hydrateList(ctx context.Context, l *models.List, withEntries bool) {
	if u, err := s.UserByID(ctx, l.UserID); err == nil {
		l.User = u
	}
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM list_likes WHERE list_id=?`, l.ID).Scan(&n)
	l.Likes = n
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE subject_type='list' AND subject_id=? AND deleted_at IS NULL`, l.ID).Scan(&n)
	l.Comments = n
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM list_entries WHERE list_id=?`, l.ID).Scan(&l.EntryCount)
	if withEntries {
		l.Entries, _ = s.GamesInList(ctx, l.ID)
	}
	frows, err := s.DB.QueryContext(ctx, `SELECT f.name FROM list_folders f JOIN list_folder_items fi ON fi.folder_id=f.id WHERE fi.list_id=?`, l.ID)
	if err == nil {
		for frows.Next() {
			var name string
			frows.Scan(&name)
			l.Folders = append(l.Folders, name)
		}
		frows.Close()
	}
}

func (s *Store) ListByID(ctx context.Context, id int64, withEntries bool) (*models.List, error) {
	l, err := scanList(s.DB.QueryRowContext(ctx, `SELECT `+listCols+` FROM lists l WHERE l.id=? AND l.deleted_at IS NULL`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.hydrateList(ctx, l, withEntries)
	return l, nil
}

func (s *Store) ListByUserSlug(ctx context.Context, userID int64, slug string, withEntries bool) (*models.List, error) {
	l, err := scanList(s.DB.QueryRowContext(ctx, `SELECT `+listCols+` FROM lists l WHERE l.user_id=? AND l.slug=? AND l.deleted_at IS NULL`, userID, slug))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.hydrateList(ctx, l, withEntries)
	return l, nil
}

func (s *Store) ListBySlugGlobal(ctx context.Context, slug string) (*models.List, error) {
	l, err := scanList(s.DB.QueryRowContext(ctx, `SELECT `+listCols+` FROM lists l WHERE l.slug=? AND l.deleted_at IS NULL ORDER BY l.id LIMIT 1`, slug))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.hydrateList(ctx, l, false)
	return l, nil
}

func (s *Store) ListsForUser(ctx context.Context, userID int64, includePrivate bool, limit, offset int) ([]*models.List, int) {
	extra := ""
	if !includePrivate {
		extra = " AND l.privacy IN ('public','unlisted')"
	}
	var total int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM lists l WHERE l.user_id=? AND l.deleted_at IS NULL`+extra, userID).Scan(&total)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+listCols+` FROM lists l WHERE l.user_id=? AND l.deleted_at IS NULL`+extra+`
		ORDER BY l.updated_at DESC LIMIT ? OFFSET ?`, userID, limit, offset)
	if err != nil {
		return nil, total
	}
	var out []*models.List
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			continue
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, total
	}
	for _, l := range out {
		s.hydrateList(ctx, l, false)
	}
	return out, total
}

// VisibleListsFor shows the owner's private lists to themselves, personal
// lists to approved followers, and public/unlisted lists to everyone.
func (s *Store) VisibleListsFor(ctx context.Context, ownerID, viewerID int64) ([]*models.List, error) {
	visibility := "l.privacy IN ('public','unlisted')"
	switch {
	case viewerID == ownerID:
		visibility = "1=1"
	case viewerID != 0:
		if ok, approved := s.IsFollowing(ctx, viewerID, ownerID); ok && approved {
			visibility = "l.privacy IN ('public','unlisted','personal')"
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+listCols+` FROM lists l WHERE l.user_id=? AND l.deleted_at IS NULL AND `+visibility+`
		ORDER BY l.updated_at DESC LIMIT 100`, ownerID)
	if err != nil {
		return nil, err
	}
	var out []*models.List
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			continue
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, l := range out {
		s.hydrateList(ctx, l, false)
	}
	return out, nil
}

func (s *Store) CreatedLists(ctx context.Context, userID, viewerID int64) ([]*models.List, error) {
	out, _ := s.ListsForUser(ctx, userID, userID == viewerID, 100, 0)
	return out, nil
}

type ListInput struct {
	UserID      int64
	Title       string
	Description string
	Tags        []string
	Privacy     string
	Ranked      bool
	Progress    bool
	DefaultSort string
	SortDir     string
	FadePlayed  bool
	DisplayMode string
	Immersive   bool
}

func (in ListInput) privacy() string {
	if in.Privacy == "" {
		return "public"
	}
	return in.Privacy
}

func (s *Store) CreateList(ctx context.Context, in ListInput) (*models.List, error) {
	slug := Slugify(in.Title)
	base := slug
	for i := 2; ; i++ {
		var n int
		s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM lists WHERE user_id=? AND slug=?`, in.UserID, slug).Scan(&n)
		if n == 0 {
			break
		}
		slug = base + "-" + itoa(i)
	}
	tags, _ := json.Marshal(in.Tags)
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO lists(user_id,slug,title,description,tags,privacy,ranked,progress,
		default_sort,sort_dir,fade_played,display_mode,immersive,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.UserID, slug, in.Title, in.Description, string(tags), in.privacy(), b2i(in.Ranked), b2i(in.Progress),
		defaultOr(in.DefaultSort, "user"), defaultOr(in.SortDir, "asc"), b2i(in.FadePlayed),
		defaultOr(in.DisplayMode, "grid"), b2i(in.Immersive), now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.ListByID(ctx, id, false)
}

func (s *Store) UpdateList(ctx context.Context, id, userID int64, in ListInput) error {
	tags, _ := json.Marshal(in.Tags)
	if in.Title != "" {
		slug := Slugify(in.Title)
		var n int
		s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM lists WHERE user_id=? AND slug=? AND id<>?`, userID, slug, id).Scan(&n)
		if n == 0 {
			_, err := s.DB.ExecContext(ctx, `UPDATE lists SET slug=? WHERE id=?`, slug, id)
			if err != nil {
				return err
			}
		}
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE lists SET title=?,description=?,tags=?,privacy=?,ranked=?,progress=?,
		default_sort=?,sort_dir=?,fade_played=?,display_mode=?,immersive=?,updated_at=? WHERE id=? AND user_id=?`,
		in.Title, in.Description, string(tags), in.privacy(), b2i(in.Ranked), b2i(in.Progress),
		defaultOr(in.DefaultSort, "user"), defaultOr(in.SortDir, "asc"), b2i(in.FadePlayed),
		defaultOr(in.DisplayMode, "grid"), b2i(in.Immersive), Now(), id, userID)
	return err
}

func (s *Store) DeleteList(ctx context.Context, id, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE lists SET deleted_at=? WHERE id=? AND user_id=?`, Now(), id, userID)
	return err
}

// ModDeleteList removes any user's list after a moderator review.
func (s *Store) ModDeleteList(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE lists SET deleted_at=? WHERE id=? AND deleted_at IS NULL`, Now(), id)
	return err
}

func (s *Store) AddListEntry(ctx context.Context, listID, gameID int64, note string) error {
	var pos int
	s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM list_entries WHERE list_id=?`, listID).Scan(&pos)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO list_entries(list_id,game_id,position,note,added_at) VALUES(?,?,?,?,?)
		ON CONFLICT(list_id,game_id) DO UPDATE SET note=excluded.note`, listID, gameID, pos, note, Now())
	if err == nil {
		s.DB.ExecContext(ctx, `UPDATE lists SET updated_at=? WHERE id=?`, Now(), listID)
	}
	return err
}

func (s *Store) RemoveListEntry(ctx context.Context, listID, gameID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM list_entries WHERE list_id=? AND game_id=?`, listID, gameID)
	if err == nil {
		s.DB.ExecContext(ctx, `UPDATE lists SET updated_at=? WHERE id=?`, Now(), listID)
	}
	return err
}

func (s *Store) ReorderList(ctx context.Context, listID int64, gameIDs []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, gid := range gameIDs {
		tx.ExecContext(ctx, `UPDATE list_entries SET position=? WHERE list_id=? AND game_id=?`, i+1, listID, gid)
	}
	tx.ExecContext(ctx, `UPDATE lists SET updated_at=? WHERE id=?`, Now(), listID)
	return tx.Commit()
}

func (s *Store) LikeList(ctx context.Context, listID, userID int64, like bool) error {
	if like {
		_, err := s.DB.ExecContext(ctx, `INSERT INTO list_likes(list_id,user_id,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, listID, userID, Now())
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM list_likes WHERE list_id=? AND user_id=?`, listID, userID)
	return err
}

func (s *Store) SearchLists(ctx context.Context, q string, limit, offset int) ([]models.List, int, error) {
	match := ftsQuery(q)
	if match == "" {
		return nil, 0, nil
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM lists l WHERE l.deleted_at IS NULL AND l.privacy='public'
		AND l.id IN (SELECT rowid FROM lists_fts WHERE lists_fts MATCH ?)`, match).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+listCols+` FROM lists l WHERE l.deleted_at IS NULL AND l.privacy='public'
		AND l.id IN (SELECT rowid FROM lists_fts WHERE lists_fts MATCH ?) ORDER BY l.updated_at DESC, l.id DESC LIMIT ? OFFSET ?`, match, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var out []models.List
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, *l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for i := range out {
		s.hydrateList(ctx, &out[i], false)
	}
	return out, total, nil
}

func (s *Store) PopularLists(ctx context.Context, limit int) ([]*models.List, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+listCols+` FROM lists l WHERE l.deleted_at IS NULL AND l.privacy='public'
		ORDER BY (SELECT COUNT(*) FROM list_likes ll WHERE ll.list_id=l.id) DESC, l.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	var out []*models.List
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			continue
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, l := range out {
		s.hydrateList(ctx, l, false)
	}
	return out, nil
}

func (s *Store) ListsContainingGame(ctx context.Context, gameID int64, limit int) ([]*models.List, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+listCols+` FROM lists l JOIN list_entries le ON le.list_id=l.id
		WHERE le.game_id=? AND l.deleted_at IS NULL AND l.privacy='public' ORDER BY l.updated_at DESC LIMIT ?`, gameID, limit)
	if err != nil {
		return nil, err
	}
	var out []*models.List
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			continue
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, l := range out {
		s.hydrateList(ctx, l, false)
	}
	return out, nil
}

// Folders

func (s *Store) Folders(ctx context.Context, userID int64) ([]models.Folder, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT f.id,f.user_id,f.name,f.sort,f.pos,
		(SELECT COUNT(*) FROM list_folder_items fi WHERE fi.folder_id=f.id) FROM list_folders f WHERE f.user_id=? ORDER BY f.pos,f.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Folder
	for rows.Next() {
		var f models.Folder
		rows.Scan(&f.ID, &f.UserID, &f.Name, &f.Sort, &f.Pos, &f.Count)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) CreateFolder(ctx context.Context, userID int64, name, sortKey string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO list_folders(user_id,name,sort,pos,created_at) VALUES(?,?,?,0,?)`, userID, name, defaultOr(sortKey, "user"), Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// OwnsFolder reports whether a folder belongs to the user.
func (s *Store) OwnsFolder(ctx context.Context, id, userID int64) bool {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM list_folders WHERE id=? AND user_id=?`, id, userID).Scan(&n)
	return n > 0
}

func (s *Store) RenameFolder(ctx context.Context, id, userID int64, name string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE list_folders SET name=? WHERE id=? AND user_id=?`, name, id, userID)
	return err
}

func (s *Store) DeleteFolder(ctx context.Context, id, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM list_folders WHERE id=? AND user_id=?`, id, userID)
	return err
}

func (s *Store) FolderLists(ctx context.Context, folderID int64) ([]*models.List, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+listCols+` FROM lists l JOIN list_folder_items fi ON fi.list_id=l.id
		WHERE fi.folder_id=? AND l.deleted_at IS NULL ORDER BY l.updated_at DESC`, folderID)
	if err != nil {
		return nil, err
	}
	var out []*models.List
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			continue
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, l := range out {
		s.hydrateList(ctx, l, false)
	}
	return out, nil
}

func (s *Store) AddListToFolder(ctx context.Context, folderID, listID int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO list_folder_items(folder_id,list_id) VALUES(?,?) ON CONFLICT DO NOTHING`, folderID, listID)
	return err
}

func (s *Store) RemoveListFromFolder(ctx context.Context, folderID, listID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM list_folder_items WHERE folder_id=? AND list_id=?`, folderID, listID)
	return err
}

func defaultOr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// Slugify makes URL-safe slugs from titles.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '-' || r == '_' || r == '\'' || r == ':' || r == '/':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "item"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// HasLikedList reports whether a user liked a list.
func (s *Store) HasLikedList(ctx context.Context, listID, userID int64) bool {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM list_likes WHERE list_id=? AND user_id=?`, listID, userID).Scan(&n)
	return n > 0
}
