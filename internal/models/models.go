// Package models holds the plain structs shared by the store and the web layer.
package models

import "time"

type User struct {
	ID                 int64
	Username           string
	Email              string
	EmailVerified      bool
	Approved           bool
	PasswordHash       string
	Bio                string
	Avatar             string
	Header             string
	Role               string // user | mod | admin
	CreatedAt          time.Time
	DeactivatedAt      *time.Time
	LastUsernameChange *time.Time
	PrivateProfile     bool
	HideActivity       bool
	ManuallyApproves   bool
	HideSocialGraph    bool
	AdultOptIn         bool
	StarScale          int
	CoverMode          string // any | mine | none
	TimeDisplay        string // always | played | click
	InfiniteScroll     bool
	Theme              string // system | dark | light
	HighContrast       bool
	Timezone           string
	FadePlayed         bool
	JournalView        string // calendar | activity | list
	CommentPrivacy     string // everyone | friends | none
	NotePrivacy        string // everyone | friends | none
	CatalogEditor      bool
	FeaturedBadge      string
	Federation         bool
	FedStatuses        bool
	FedReviews         bool
	FedLists           bool
	RemoteActor        string
}

func (u *User) IsAdmin() bool { return u != nil && u.Role == "admin" }

func (u *User) CanModerate() bool { return u != nil && (u.Role == "mod" || u.Role == "admin") }

func (u *User) CanEditCatalog() bool {
	return u != nil && (u.CatalogEditor || u.CanModerate())
}

func (u *User) RoleLabel() string {
	if u == nil {
		return ""
	}
	switch u.Role {
	case "admin":
		return "admin"
	case "mod":
		return "moderator"
	default:
		return "user"
	}
}

type SocialLink struct {
	Site string
	URL  string
}

type Game struct {
	ID            int64
	IGDBID        int64
	Slug          string
	Title         string
	SortTitle     string
	Summary       string
	CoverURL      string
	BannerURL     string
	ReleaseDate   string
	ReleaseYear   int
	ReleaseStatus string
	Category      string // main_game, dlc_addon, expansion, ...
	ParentID      *int64
	ParentIGDBID  int64 // IGDB id of the parent game, used while importing editions
	EditionName   string
	Franchise     string
	Adult         bool
	Restricted    bool
	AvgRating     float64
	RatingCount   int
	Weighted      float64
	Popularity    int
	Plays         int
	Completions   int
	Backlogs      int
	Wishlists     int
	Playing       int
	Likes         int
	ListsCount    int
	Reviews       int
	TimeToBeat    *TimeToBeat
	UpdatedAt     time.Time
	SyncedAt      *time.Time
	Platforms     []Platform
	Genres        []string
	Themes        []string
	Modes         []string
	Companies     []CompanyRole
	Screenshots   []string
	Videos        []string
	AltNames      []string
	ReleaseDates  []ReleaseDate
	Contents      []Game // bundle contents
	Bundles       []Game
	Editions      []Game
	Parents       []*Game // ancestor chain, original first
	Related       []Game
	Updates       []Game
	Remakes       []Game
	Ports         []Game
	Series        []Game
}

// TimeToBeat holds IGDB's average completion times in seconds.
type TimeToBeat struct {
	Hastily    int
	Normally   int
	Completely int
}

func (t *TimeToBeat) HasData() bool {
	return t != nil && (t.Hastily > 0 || t.Normally > 0 || t.Completely > 0)
}

type CompanyRole struct {
	Name string
	Role string // developer | publisher | porting | supporting
}

type Platform struct {
	ID         int64
	IGDBID     int64
	Name       string
	Abbrev     string
	Generation int
	Family     string
}

type ReleaseDate struct {
	PlatformID int64
	Platform   string
	Region     string
	Date       string
	Human      string
	Status     string
}

type Log struct {
	ID            int64
	UserID        int64
	GameID        int64
	Game          *Game
	User          *User
	Name          string
	Rating        float64 // 0 means unrated; stored in half stars
	Review        string
	ReviewHTML    string
	Spoiler       bool
	Status        string
	Replay        bool
	Mastered      bool
	Difficulty    string
	PlatformID    *int64
	PlatformName  string
	PlayedOnID    *int64
	PlayedOnName  string
	Ownership     string
	Storefront    string
	EditionGameID *int64
	EditionName   string
	StartedAt     string
	FinishedAt    string
	TimeToFinish  float64
	TimeToMaster  float64
	Playtime      float64
	SyncSessions  bool
	PrimaryRating bool
	Language      string
	Draft         bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Likes         int
	Liked         bool
	CommentCount  int
	UserRating    float64 // current user's rating of the game
}

type Session struct {
	ID          int64
	LogID       int64
	Note        string
	NoteHTML    string
	NotePrivacy string
	Playtime    float64
	StartedOn   string
	EndedOn     string
	Days        []string // YYYY-MM-DD
	Tags        []string
	CreatedAt   time.Time
}

type LibraryEntry struct {
	ID         int64
	UserID     int64
	GameID     int64
	Game       *Game
	PlatformID *int64
	Platform   string
	Ownership  string
	Storefront string
	Edition    string
	CreatedAt  time.Time
}

type Status struct {
	UserID    int64
	GameID    int64
	Played    int
	Playing   bool
	Backlog   bool
	Wishlist  bool
	Liked     bool
	PlayedAt  time.Time
	PlayingAt time.Time
	UpdatedAt time.Time
}

type List struct {
	ID          int64
	UserID      int64
	User        *User
	Slug        string
	Title       string
	Description string
	DescHTML    string
	Tags        []string
	Privacy     string
	Ranked      bool
	Progress    bool
	DefaultSort string
	SortDir     string
	FadePlayed  bool
	DisplayMode string
	Immersive   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Likes       int
	Liked       bool
	Comments    int
	EntryCount  int
	APID        string
	Entries     []ListEntry
	Folders     []string
}

type ListEntry struct {
	ListID   int64
	GameID   int64
	Game     *Game
	Position int
	Note     string
	AddedAt  time.Time
}

type Folder struct {
	ID     int64
	UserID int64
	Name   string
	Sort   string
	Pos    int
	Count  int
}

type Comment struct {
	ID           int64
	UserID       *int64
	User         *User
	RemoteActor  string
	RemoteName   string
	RemoteAvatar string
	Body         string
	BodyHTML     string
	Deleted      bool
	CreatedAt    time.Time
	ReplyCount   int
	Replies      []*Comment
	ParentID     *int64
	SubjectType  string
	SubjectID    int64
	Remote       bool
}

type Notification struct {
	ID          int64
	UserID      int64
	Type        string
	ActorID     *int64
	Actor       *User
	RemoteActor string
	ObjectType  string
	ObjectID    int64
	Text        string
	URL         string
	ReadAt      *time.Time
	CreatedAt   time.Time
	GroupKey    string
}

type Activity struct {
	ID         int64
	UserID     int64
	User       *User
	Type       string
	GameID     *int64
	Game       *Game
	LogID      *int64
	ListID     *int64
	TargetName string
	Visibility string
	CreatedAt  time.Time
}

type RemoteNote struct {
	ID          int64
	APID        string
	ActorURI    string
	ActorName   string
	ActorAvatar string
	ContentHTML string
	GameIGDBID  int64
	GameTitle   string
	GameURL     string
	Rating      float64
	InReplyTo   string
	PublishedAt time.Time
	URL         string
}

type Badge struct {
	Key      string
	Title    string
	Desc     string
	EarnedAt *time.Time
}

type Report struct {
	ID         int64
	ReporterID *int64
	TargetType string
	TargetID   int64
	TargetURL  string
	Reason     string
	Details    string
	Status     string
	CreatedAt  time.Time
	Reporter   string
}

type News struct {
	ID        int64
	Title     string
	Body      string
	BodyHTML  string
	CreatedAt time.Time
}

type ModAction struct {
	ID          int64
	ModeratorID *int64
	Moderator   string
	TargetUser  *int64
	TargetName  string
	Action      string
	Reason      string
	ReportID    *int64
	CreatedAt   time.Time
}

type Job struct {
	ID       int64
	Kind     string
	Payload  string
	RunAt    time.Time
	Attempts int
	LastErr  string
}
