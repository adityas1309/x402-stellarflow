package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Store provides all functions to execute db queries and transactions
type Store struct {
	db *sql.DB
}

// NewStore creates a new Store
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying *sql.DB (for advanced queries in adapters)
func (s *Store) DB() *sql.DB {
	return s.db
}

// execTx executes a function within a database transaction
func (s *Store) execTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	err = fn(tx)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("tx err: %v, rb err: %v", err, rbErr)
		}
		return err
	}

	return tx.Commit()
}

// ---- Organization types ----

type Organization struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Slug      string         `json:"slug"`
	LogoURL   sql.NullString `json:"logo_url"`
	Plan      string         `json:"plan"`
	Credits   int32          `json:"credits"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type CreateOrganizationParams struct {
	Name    string
	Slug    string
	LogoURL sql.NullString
	Plan    string
	Credits int32
}

func (s *Store) CreateOrganization(ctx context.Context, arg CreateOrganizationParams) (Organization, error) {
	var org Organization
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO organizations (name, slug, logo_url, plan, credits)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, name, slug, logo_url, plan, credits, created_at, updated_at`,
		arg.Name, arg.Slug, arg.LogoURL, arg.Plan, arg.Credits,
	)
	err := row.Scan(&org.ID, &org.Name, &org.Slug, &org.LogoURL, &org.Plan, &org.Credits, &org.CreatedAt, &org.UpdatedAt)
	return org, err
}

func (s *Store) GetOrganizationByID(ctx context.Context, id int64) (Organization, error) {
	var org Organization
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, logo_url, plan, credits, created_at, updated_at FROM organizations WHERE id = $1`,
		id,
	)
	err := row.Scan(&org.ID, &org.Name, &org.Slug, &org.LogoURL, &org.Plan, &org.Credits, &org.CreatedAt, &org.UpdatedAt)
	return org, err
}

func (s *Store) GetOrganizationBySlug(ctx context.Context, slug string) (Organization, error) {
	var org Organization
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, logo_url, plan, credits, created_at, updated_at FROM organizations WHERE slug = $1`,
		slug,
	)
	err := row.Scan(&org.ID, &org.Name, &org.Slug, &org.LogoURL, &org.Plan, &org.Credits, &org.CreatedAt, &org.UpdatedAt)
	return org, err
}

// ---- User types ----

type User struct {
	ID             int64     `json:"id"`
	OrgID          int64     `json:"org_id"`
	Email          string    `json:"email"`
	HashedPassword string    `json:"-"`
	FullName       string    `json:"full_name"`
	Role           string    `json:"role"`
	IsSuperAdmin   bool      `json:"is_super_admin"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type CreateUserParams struct {
	OrgID          int64
	Email          string
	HashedPassword string
	FullName       string
	Role           string
}

func (s *Store) CreateUser(ctx context.Context, arg CreateUserParams) (User, error) {
	var u User
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO users (org_id, email, hashed_password, full_name, role)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, org_id, email, hashed_password, full_name, role, is_super_admin, created_at, updated_at`,
		arg.OrgID, arg.Email, arg.HashedPassword, arg.FullName, arg.Role,
	)
	err := row.Scan(&u.ID, &u.OrgID, &u.Email, &u.HashedPassword, &u.FullName, &u.Role, &u.IsSuperAdmin, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, email, hashed_password, full_name, role, is_super_admin, created_at, updated_at FROM users WHERE email = $1`,
		email,
	)
	err := row.Scan(&u.ID, &u.OrgID, &u.Email, &u.HashedPassword, &u.FullName, &u.Role, &u.IsSuperAdmin, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (User, error) {
	var u User
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, email, hashed_password, full_name, role, is_super_admin, created_at, updated_at FROM users WHERE id = $1`,
		id,
	)
	err := row.Scan(&u.ID, &u.OrgID, &u.Email, &u.HashedPassword, &u.FullName, &u.Role, &u.IsSuperAdmin, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func (s *Store) ListUsersByOrg(ctx context.Context, orgID int64) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, email, hashed_password, full_name, role, is_super_admin, created_at, updated_at
		 FROM users WHERE org_id = $1 ORDER BY created_at ASC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.OrgID, &u.Email, &u.HashedPassword, &u.FullName, &u.Role, &u.IsSuperAdmin, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) DeleteUser(ctx context.Context, id int64, orgID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1 AND org_id = $2`, id, orgID)
	return err
}

// ---- Competitor types ----

type Competitor struct {
	ID            int64          `json:"id"`
	OrgID         int64          `json:"org_id"`
	Platform      string         `json:"platform"`
	Username      string         `json:"username"`
	DisplayName   sql.NullString `json:"display_name"`
	ProfilePicURL sql.NullString `json:"profile_pic_url"`
	IsOwnAccount  bool           `json:"is_own_account"`
	ScrapeStatus  string         `json:"scrape_status"`
	ScrapeError   sql.NullString `json:"scrape_error"`
	LastScrapedAt sql.NullTime   `json:"last_scraped_at"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type CreateCompetitorParams struct {
	OrgID         int64
	Platform      string
	Username      string
	DisplayName   sql.NullString
	ProfilePicURL sql.NullString
	IsOwnAccount  bool
}

func (s *Store) CreateCompetitor(ctx context.Context, arg CreateCompetitorParams) (Competitor, error) {
	var c Competitor
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO competitors (org_id, platform, username, display_name, profile_pic_url, is_own_account)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, org_id, platform, username, display_name, profile_pic_url, is_own_account, scrape_status, scrape_error, last_scraped_at, created_at, updated_at`,
		arg.OrgID, arg.Platform, arg.Username, arg.DisplayName, arg.ProfilePicURL, arg.IsOwnAccount,
	)
	err := row.Scan(&c.ID, &c.OrgID, &c.Platform, &c.Username, &c.DisplayName, &c.ProfilePicURL,
		&c.IsOwnAccount, &c.ScrapeStatus, &c.ScrapeError, &c.LastScrapedAt, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *Store) ListCompetitors(ctx context.Context, orgID int64) ([]Competitor, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, platform, username, display_name, profile_pic_url, is_own_account, scrape_status, scrape_error, last_scraped_at, created_at, updated_at
		 FROM competitors WHERE org_id = $1 ORDER BY is_own_account DESC, created_at ASC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var competitors []Competitor
	for rows.Next() {
		var c Competitor
		if err := rows.Scan(&c.ID, &c.OrgID, &c.Platform, &c.Username, &c.DisplayName, &c.ProfilePicURL,
			&c.IsOwnAccount, &c.ScrapeStatus, &c.ScrapeError, &c.LastScrapedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		competitors = append(competitors, c)
	}
	return competitors, rows.Err()
}

func (s *Store) GetCompetitorByID(ctx context.Context, id int64, orgID int64) (Competitor, error) {
	var c Competitor
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, platform, username, display_name, profile_pic_url, is_own_account, scrape_status, scrape_error, last_scraped_at, created_at, updated_at
		 FROM competitors WHERE id = $1 AND org_id = $2`,
		id, orgID,
	)
	err := row.Scan(&c.ID, &c.OrgID, &c.Platform, &c.Username, &c.DisplayName, &c.ProfilePicURL,
		&c.IsOwnAccount, &c.ScrapeStatus, &c.ScrapeError, &c.LastScrapedAt, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *Store) GetCompetitorByIDNoOrg(ctx context.Context, id int64) (Competitor, error) {
	var c Competitor
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, platform, username, display_name, profile_pic_url, is_own_account, scrape_status, scrape_error, last_scraped_at, created_at, updated_at
		 FROM competitors WHERE id = $1`,
		id,
	)
	err := row.Scan(&c.ID, &c.OrgID, &c.Platform, &c.Username, &c.DisplayName, &c.ProfilePicURL,
		&c.IsOwnAccount, &c.ScrapeStatus, &c.ScrapeError, &c.LastScrapedAt, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

type UpdateCompetitorStatusParams struct {
	ID            int64
	ScrapeStatus  string
	ScrapeError   sql.NullString
	LastScrapedAt sql.NullTime
}

func (s *Store) UpdateCompetitorStatus(ctx context.Context, arg UpdateCompetitorStatusParams) (Competitor, error) {
	var c Competitor
	row := s.db.QueryRowContext(ctx,
		`UPDATE competitors
		 SET scrape_status = $2, scrape_error = $3, last_scraped_at = $4, updated_at = NOW()
		 WHERE id = $1
		 RETURNING id, org_id, platform, username, display_name, profile_pic_url, is_own_account, scrape_status, scrape_error, last_scraped_at, created_at, updated_at`,
		arg.ID, arg.ScrapeStatus, arg.ScrapeError, arg.LastScrapedAt,
	)
	err := row.Scan(&c.ID, &c.OrgID, &c.Platform, &c.Username, &c.DisplayName, &c.ProfilePicURL,
		&c.IsOwnAccount, &c.ScrapeStatus, &c.ScrapeError, &c.LastScrapedAt, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

type UpdateCompetitorProfileParams struct {
	ID            int64
	DisplayName   sql.NullString
	ProfilePicURL sql.NullString
}

func (s *Store) UpdateCompetitorProfile(ctx context.Context, arg UpdateCompetitorProfileParams) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE competitors SET display_name = $2, profile_pic_url = $3, updated_at = NOW() WHERE id = $1`,
		arg.ID, arg.DisplayName, arg.ProfilePicURL,
	)
	return err
}

func (s *Store) DeleteCompetitor(ctx context.Context, id int64, orgID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM competitors WHERE id = $1 AND org_id = $2`,
		id, orgID,
	)
	return err
}

// ---- Competitor Metrics types ----

type CompetitorMetrics struct {
	ID              int64           `json:"id"`
	CompetitorID    int64           `json:"competitor_id"`
	FollowersCount  int32           `json:"followers_count"`
	FollowingCount  int32           `json:"following_count"`
	PostsCount      int32           `json:"posts_count"`
	TotalLikes      int64           `json:"total_likes"`
	EngagementRate  int64           `json:"engagement_rate"`
	IsVerified      bool            `json:"is_verified"`
	PostsPerWeek    float64         `json:"posts_per_week"`
	Bio             sql.NullString  `json:"bio"`
	Website         sql.NullString  `json:"website"`
	RawProfileData  json.RawMessage `json:"raw_profile_data"`
	RawPostsData    json.RawMessage `json:"raw_posts_data"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type UpsertCompetitorMetricsParams struct {
	CompetitorID   int64
	FollowersCount int32
	FollowingCount int32
	PostsCount     int32
	TotalLikes     int64
	EngagementRate int64
	IsVerified     bool
	PostsPerWeek   float64
	Bio            sql.NullString
	Website        sql.NullString
	RawProfileData json.RawMessage
	RawPostsData   json.RawMessage
}

func (s *Store) UpsertCompetitorMetrics(ctx context.Context, arg UpsertCompetitorMetricsParams) (CompetitorMetrics, error) {
	var m CompetitorMetrics

	rawProfile := arg.RawProfileData
	if rawProfile == nil {
		rawProfile = json.RawMessage(`{}`)
	}
	rawPosts := arg.RawPostsData
	if rawPosts == nil {
		rawPosts = json.RawMessage(`{}`)
	}

	row := s.db.QueryRowContext(ctx,
		`INSERT INTO competitor_metrics (competitor_id, followers_count, following_count, posts_count, total_likes, engagement_rate, is_verified, posts_per_week, bio, website, raw_profile_data, raw_posts_data)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT (competitor_id) DO UPDATE
		 SET followers_count = EXCLUDED.followers_count,
		     following_count = EXCLUDED.following_count,
		     posts_count = EXCLUDED.posts_count,
		     total_likes = EXCLUDED.total_likes,
		     engagement_rate = EXCLUDED.engagement_rate,
		     is_verified = EXCLUDED.is_verified,
		     posts_per_week = EXCLUDED.posts_per_week,
		     bio = EXCLUDED.bio,
		     website = EXCLUDED.website,
		     raw_profile_data = EXCLUDED.raw_profile_data,
		     raw_posts_data = EXCLUDED.raw_posts_data,
		     updated_at = NOW()
		 RETURNING id, competitor_id, followers_count, following_count, posts_count, total_likes, engagement_rate, is_verified, posts_per_week, bio, website, raw_profile_data, raw_posts_data, created_at, updated_at`,
		arg.CompetitorID, arg.FollowersCount, arg.FollowingCount, arg.PostsCount, arg.TotalLikes,
		arg.EngagementRate, arg.IsVerified, arg.PostsPerWeek, arg.Bio, arg.Website, rawProfile, rawPosts,
	)
	err := row.Scan(&m.ID, &m.CompetitorID, &m.FollowersCount, &m.FollowingCount, &m.PostsCount,
		&m.TotalLikes, &m.EngagementRate, &m.IsVerified, &m.PostsPerWeek, &m.Bio, &m.Website,
		&m.RawProfileData, &m.RawPostsData, &m.CreatedAt, &m.UpdatedAt)
	return m, err
}

func (s *Store) GetCompetitorMetrics(ctx context.Context, competitorID int64) (CompetitorMetrics, error) {
	var m CompetitorMetrics
	row := s.db.QueryRowContext(ctx,
		`SELECT id, competitor_id, followers_count, following_count, posts_count, total_likes, engagement_rate, is_verified, posts_per_week, bio, website, raw_profile_data, raw_posts_data, created_at, updated_at
		 FROM competitor_metrics WHERE competitor_id = $1`,
		competitorID,
	)
	err := row.Scan(&m.ID, &m.CompetitorID, &m.FollowersCount, &m.FollowingCount, &m.PostsCount,
		&m.TotalLikes, &m.EngagementRate, &m.IsVerified, &m.PostsPerWeek, &m.Bio, &m.Website,
		&m.RawProfileData, &m.RawPostsData, &m.CreatedAt, &m.UpdatedAt)
	return m, err
}

func (s *Store) ListCompetitorMetricsByOrg(ctx context.Context, orgID int64) ([]CompetitorMetrics, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cm.id, cm.competitor_id, cm.followers_count, cm.following_count, cm.posts_count, cm.total_likes, cm.engagement_rate, cm.is_verified, cm.posts_per_week, cm.bio, cm.website, cm.raw_profile_data, cm.raw_posts_data, cm.created_at, cm.updated_at
		 FROM competitor_metrics cm
		 JOIN competitors c ON c.id = cm.competitor_id
		 WHERE c.org_id = $1`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []CompetitorMetrics
	for rows.Next() {
		var m CompetitorMetrics
		if err := rows.Scan(&m.ID, &m.CompetitorID, &m.FollowersCount, &m.FollowingCount, &m.PostsCount,
			&m.TotalLikes, &m.EngagementRate, &m.IsVerified, &m.PostsPerWeek, &m.Bio, &m.Website,
			&m.RawProfileData, &m.RawPostsData, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// ---- Metrics History types ----

type MetricsHistory struct {
	ID             int64     `json:"id"`
	CompetitorID   int64     `json:"competitor_id"`
	FollowersCount int32     `json:"followers_count"`
	EngagementRate int64     `json:"engagement_rate"`
	PostsCount     int32     `json:"posts_count"`
	ScrapedAt      time.Time `json:"scraped_at"`
}

type InsertMetricsHistoryParams struct {
	CompetitorID   int64
	FollowersCount int32
	EngagementRate int64
	PostsCount     int32
}

func (s *Store) InsertMetricsHistory(ctx context.Context, arg InsertMetricsHistoryParams) (MetricsHistory, error) {
	var h MetricsHistory
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO metrics_history (competitor_id, followers_count, engagement_rate, posts_count)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, competitor_id, followers_count, engagement_rate, posts_count, scraped_at`,
		arg.CompetitorID, arg.FollowersCount, arg.EngagementRate, arg.PostsCount,
	)
	err := row.Scan(&h.ID, &h.CompetitorID, &h.FollowersCount, &h.EngagementRate, &h.PostsCount, &h.ScrapedAt)
	return h, err
}

func (s *Store) GetMetricsHistory(ctx context.Context, competitorID int64, limit int32) ([]MetricsHistory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, competitor_id, followers_count, engagement_rate, posts_count, scraped_at
		 FROM metrics_history WHERE competitor_id = $1 ORDER BY scraped_at DESC LIMIT $2`,
		competitorID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []MetricsHistory
	for rows.Next() {
		var h MetricsHistory
		if err := rows.Scan(&h.ID, &h.CompetitorID, &h.FollowersCount, &h.EngagementRate, &h.PostsCount, &h.ScrapedAt); err != nil {
			return nil, err
		}
		history = append(history, h)
	}
	return history, rows.Err()
}

// ---- Org Insights ----

type OrgInsight struct {
	ID        int64           `json:"id"`
	OrgID     int64           `json:"org_id"`
	Language  string          `json:"language"`
	Status    string          `json:"status"`
	ErrorMsg  sql.NullString  `json:"error_msg"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (s *Store) UpsertOrgInsight(ctx context.Context, orgID int64, language string) (OrgInsight, error) {
	var ins OrgInsight
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO org_insights (org_id, language, status)
		 VALUES ($1, $2, 'pending')
		 ON CONFLICT (org_id, language) DO UPDATE SET status = 'pending', updated_at = NOW()
		 RETURNING id, org_id, language, status, error_msg, data, created_at, updated_at`,
		orgID, language,
	)
	err := row.Scan(&ins.ID, &ins.OrgID, &ins.Language, &ins.Status, &ins.ErrorMsg, &ins.Data, &ins.CreatedAt, &ins.UpdatedAt)
	return ins, err
}

func (s *Store) GetOrgInsight(ctx context.Context, orgID int64, language string) (OrgInsight, error) {
	var ins OrgInsight
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, language, status, error_msg, data, created_at, updated_at
		 FROM org_insights WHERE org_id = $1 AND language = $2`,
		orgID, language,
	)
	err := row.Scan(&ins.ID, &ins.OrgID, &ins.Language, &ins.Status, &ins.ErrorMsg, &ins.Data, &ins.CreatedAt, &ins.UpdatedAt)
	return ins, err
}

func (s *Store) UpdateOrgInsightStatus(ctx context.Context, id int64, status string, errorMsg sql.NullString, data json.RawMessage) error {
	if data == nil {
		data = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE org_insights SET status = $2, error_msg = $3, data = $4, updated_at = NOW() WHERE id = $1`,
		id, status, errorMsg, data,
	)
	return err
}

// ---- Content Recommendations ----

type ContentRecommendations struct {
	ID        int64           `json:"id"`
	OrgID     int64           `json:"org_id"`
	Language  string          `json:"language"`
	Status    string          `json:"status"`
	ErrorMsg  sql.NullString  `json:"error_msg"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (s *Store) UpsertContentRecommendations(ctx context.Context, orgID int64, language string) (ContentRecommendations, error) {
	var cr ContentRecommendations
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO content_recommendations (org_id, language, status)
		 VALUES ($1, $2, 'pending')
		 ON CONFLICT (org_id, language) DO UPDATE SET status = 'pending', updated_at = NOW()
		 RETURNING id, org_id, language, status, error_msg, data, created_at, updated_at`,
		orgID, language,
	)
	err := row.Scan(&cr.ID, &cr.OrgID, &cr.Language, &cr.Status, &cr.ErrorMsg, &cr.Data, &cr.CreatedAt, &cr.UpdatedAt)
	return cr, err
}

func (s *Store) GetContentRecommendations(ctx context.Context, orgID int64, language string) (ContentRecommendations, error) {
	var cr ContentRecommendations
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, language, status, error_msg, data, created_at, updated_at
		 FROM content_recommendations WHERE org_id = $1 AND language = $2`,
		orgID, language,
	)
	err := row.Scan(&cr.ID, &cr.OrgID, &cr.Language, &cr.Status, &cr.ErrorMsg, &cr.Data, &cr.CreatedAt, &cr.UpdatedAt)
	return cr, err
}

func (s *Store) UpdateContentRecommendationsStatus(ctx context.Context, id int64, status string, errorMsg sql.NullString, data json.RawMessage) error {
	if data == nil {
		data = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE content_recommendations SET status = $2, error_msg = $3, data = $4, updated_at = NOW() WHERE id = $1`,
		id, status, errorMsg, data,
	)
	return err
}

// ---- Content Pillars ----

type ContentPillars struct {
	ID        int64           `json:"id"`
	OrgID     int64           `json:"org_id"`
	Language  string          `json:"language"`
	Status    string          `json:"status"`
	ErrorMsg  sql.NullString  `json:"error_msg"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (s *Store) UpsertContentPillars(ctx context.Context, orgID int64, language string) (ContentPillars, error) {
	var cp ContentPillars
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO content_pillars (org_id, language, status)
		 VALUES ($1, $2, 'pending')
		 ON CONFLICT (org_id, language) DO UPDATE SET status = 'pending', updated_at = NOW()
		 RETURNING id, org_id, language, status, error_msg, data, created_at, updated_at`,
		orgID, language,
	)
	err := row.Scan(&cp.ID, &cp.OrgID, &cp.Language, &cp.Status, &cp.ErrorMsg, &cp.Data, &cp.CreatedAt, &cp.UpdatedAt)
	return cp, err
}

func (s *Store) GetContentPillars(ctx context.Context, orgID int64, language string) (ContentPillars, error) {
	var cp ContentPillars
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, language, status, error_msg, data, created_at, updated_at
		 FROM content_pillars WHERE org_id = $1 AND language = $2`,
		orgID, language,
	)
	err := row.Scan(&cp.ID, &cp.OrgID, &cp.Language, &cp.Status, &cp.ErrorMsg, &cp.Data, &cp.CreatedAt, &cp.UpdatedAt)
	return cp, err
}

func (s *Store) UpdateContentPillarsStatus(ctx context.Context, id int64, status string, errorMsg sql.NullString, data json.RawMessage) error {
	if data == nil {
		data = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE content_pillars SET status = $2, error_msg = $3, data = $4, updated_at = NOW() WHERE id = $1`,
		id, status, errorMsg, data,
	)
	return err
}

// ---- Heatmap Explanations ----

type HeatmapExplanation struct {
	ID        int64           `json:"id"`
	OrgID     int64           `json:"org_id"`
	Language  string          `json:"language"`
	Status    string          `json:"status"`
	ErrorMsg  sql.NullString  `json:"error_msg"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (s *Store) UpsertHeatmapExplanation(ctx context.Context, orgID int64, language string) (HeatmapExplanation, error) {
	var h HeatmapExplanation
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO heatmap_explanations (org_id, language, status)
		 VALUES ($1, $2, 'pending')
		 ON CONFLICT (org_id, language) DO UPDATE SET status = 'pending', updated_at = NOW()
		 RETURNING id, org_id, language, status, error_msg, data, created_at, updated_at`,
		orgID, language,
	)
	err := row.Scan(&h.ID, &h.OrgID, &h.Language, &h.Status, &h.ErrorMsg, &h.Data, &h.CreatedAt, &h.UpdatedAt)
	return h, err
}

func (s *Store) GetHeatmapExplanation(ctx context.Context, orgID int64, language string) (HeatmapExplanation, error) {
	var h HeatmapExplanation
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, language, status, error_msg, data, created_at, updated_at
		 FROM heatmap_explanations WHERE org_id = $1 AND language = $2`,
		orgID, language,
	)
	err := row.Scan(&h.ID, &h.OrgID, &h.Language, &h.Status, &h.ErrorMsg, &h.Data, &h.CreatedAt, &h.UpdatedAt)
	return h, err
}

func (s *Store) UpdateHeatmapExplanationStatus(ctx context.Context, id int64, status string, errorMsg sql.NullString, data json.RawMessage) error {
	if data == nil {
		data = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE heatmap_explanations SET status = $2, error_msg = $3, data = $4, updated_at = NOW() WHERE id = $1`,
		id, status, errorMsg, data,
	)
	return err
}

// ---- Report types ----

type Report struct {
	ID              int64           `json:"id"`
	OrgID           int64           `json:"org_id"`
	CreatedBy       int64           `json:"created_by"`
	Title           string          `json:"title"`
	PeriodDays      int32           `json:"period_days"`
	Language        string          `json:"language"`
	IncludeBranding bool            `json:"include_branding"`
	CompetitorIDs   pq.Int64Array   `json:"competitor_ids"`
	Sections        pq.StringArray  `json:"sections"`
	Status          string          `json:"status"`
	ErrorMessage    sql.NullString  `json:"error_message"`
	ResultData      json.RawMessage `json:"result_data"`
	CreditsUsed     int32           `json:"credits_used"`
	CreatedAt       time.Time       `json:"created_at"`
	CompletedAt     sql.NullTime    `json:"completed_at"`
}

type CreateReportParams struct {
	OrgID           int64
	CreatedBy       int64
	Title           string
	PeriodDays      int32
	Language        string
	IncludeBranding bool
	CompetitorIDs   pq.Int64Array
	Sections        pq.StringArray
	CreditsUsed     int32
}

func (s *Store) CreateReport(ctx context.Context, arg CreateReportParams) (Report, error) {
	var r Report
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO reports (org_id, created_by, title, period_days, language, include_branding, competitor_ids, sections, credits_used)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, org_id, created_by, title, period_days, language, include_branding, competitor_ids, sections, status, error_message, result_data, credits_used, created_at, completed_at`,
		arg.OrgID, arg.CreatedBy, arg.Title, arg.PeriodDays, arg.Language, arg.IncludeBranding,
		arg.CompetitorIDs, arg.Sections, arg.CreditsUsed,
	)
	err := row.Scan(&r.ID, &r.OrgID, &r.CreatedBy, &r.Title, &r.PeriodDays, &r.Language, &r.IncludeBranding,
		&r.CompetitorIDs, &r.Sections, &r.Status, &r.ErrorMessage, &r.ResultData, &r.CreditsUsed,
		&r.CreatedAt, &r.CompletedAt)
	return r, err
}

type ListReportsParams struct {
	OrgID  int64
	Limit  int32
	Offset int32
}

func (s *Store) ListReports(ctx context.Context, arg ListReportsParams) ([]Report, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, created_by, title, period_days, language, include_branding, competitor_ids, sections, status, error_message, result_data, credits_used, created_at, completed_at
		 FROM reports WHERE org_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		arg.OrgID, arg.Limit, arg.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reports []Report
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.OrgID, &r.CreatedBy, &r.Title, &r.PeriodDays, &r.Language, &r.IncludeBranding,
			&r.CompetitorIDs, &r.Sections, &r.Status, &r.ErrorMessage, &r.ResultData, &r.CreditsUsed,
			&r.CreatedAt, &r.CompletedAt); err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

func (s *Store) CountReports(ctx context.Context, orgID int64) (int64, error) {
	var count int64
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports WHERE org_id = $1`, orgID)
	err := row.Scan(&count)
	return count, err
}

func (s *Store) GetReportByID(ctx context.Context, id int64, orgID int64) (Report, error) {
	var r Report
	row := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, created_by, title, period_days, language, include_branding, competitor_ids, sections, status, error_message, result_data, credits_used, created_at, completed_at
		 FROM reports WHERE id = $1 AND org_id = $2`,
		id, orgID,
	)
	err := row.Scan(&r.ID, &r.OrgID, &r.CreatedBy, &r.Title, &r.PeriodDays, &r.Language, &r.IncludeBranding,
		&r.CompetitorIDs, &r.Sections, &r.Status, &r.ErrorMessage, &r.ResultData, &r.CreditsUsed,
		&r.CreatedAt, &r.CompletedAt)
	return r, err
}

type UpdateReportStatusParams struct {
	ID           int64
	Status       string
	ErrorMessage sql.NullString
	ResultData   json.RawMessage
	CompletedAt  sql.NullTime
}

func (s *Store) UpdateReportStatus(ctx context.Context, arg UpdateReportStatusParams) (Report, error) {
	var r Report

	resultData := arg.ResultData
	if resultData == nil {
		resultData = json.RawMessage(`{}`)
	}

	row := s.db.QueryRowContext(ctx,
		`UPDATE reports
		 SET status = $2, error_message = $3, result_data = $4, completed_at = $5
		 WHERE id = $1
		 RETURNING id, org_id, created_by, title, period_days, language, include_branding, competitor_ids, sections, status, error_message, result_data, credits_used, created_at, completed_at`,
		arg.ID, arg.Status, arg.ErrorMessage, resultData, arg.CompletedAt,
	)
	err := row.Scan(&r.ID, &r.OrgID, &r.CreatedBy, &r.Title, &r.PeriodDays, &r.Language, &r.IncludeBranding,
		&r.CompetitorIDs, &r.Sections, &r.Status, &r.ErrorMessage, &r.ResultData, &r.CreditsUsed,
		&r.CreatedAt, &r.CompletedAt)
	return r, err
}

func (s *Store) DeleteReport(ctx context.Context, id int64, orgID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM reports WHERE id = $1 AND org_id = $2`, id, orgID)
	return err
}

// ---- Credit operations ----

func (s *Store) GetOrgCredits(ctx context.Context, orgID int64) (int32, error) {
	var credits int32
	row := s.db.QueryRowContext(ctx, `SELECT credits FROM organizations WHERE id = $1`, orgID)
	err := row.Scan(&credits)
	return credits, err
}

type DeductCreditParams struct {
	OrgID       int64
	UserID      int64
	ReportID    sql.NullInt64
	Amount      int32
	Description string
}

// DeductCredit atomically deducts credits and records the transaction
func (s *Store) DeductCredit(ctx context.Context, arg DeductCreditParams) error {
	return s.execTx(ctx, func(tx *sql.Tx) error {
		// Lock and fetch current credits
		var credits int32
		row := tx.QueryRowContext(ctx,
			`SELECT credits FROM organizations WHERE id = $1 FOR UPDATE`,
			arg.OrgID,
		)
		if err := row.Scan(&credits); err != nil {
			return err
		}

		if credits < arg.Amount {
			return fmt.Errorf("insufficient credits: have %d, need %d", credits, arg.Amount)
		}

		newBalance := credits - arg.Amount

		// Update org credits
		_, err := tx.ExecContext(ctx,
			`UPDATE organizations SET credits = $2, updated_at = NOW() WHERE id = $1`,
			arg.OrgID, newBalance,
		)
		if err != nil {
			return err
		}

		// Insert credit transaction
		_, err = tx.ExecContext(ctx,
			`INSERT INTO credit_transactions (org_id, user_id, report_id, amount, balance, description)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			arg.OrgID, arg.UserID, arg.ReportID, -int32(arg.Amount), newBalance, arg.Description,
		)
		return err
	})
}

type AddCreditTransactionParams struct {
	OrgID       int64
	UserID      int64
	ReportID    sql.NullInt64
	Amount      int32
	Description string
}

// AddCreditTransaction atomically adds credits and records the transaction
func (s *Store) AddCreditTransaction(ctx context.Context, arg AddCreditTransactionParams) error {
	return s.execTx(ctx, func(tx *sql.Tx) error {
		var credits int32
		row := tx.QueryRowContext(ctx,
			`SELECT credits FROM organizations WHERE id = $1 FOR UPDATE`,
			arg.OrgID,
		)
		if err := row.Scan(&credits); err != nil {
			return err
		}

		newBalance := credits + arg.Amount

		_, err := tx.ExecContext(ctx,
			`UPDATE organizations SET credits = $2, updated_at = NOW() WHERE id = $1`,
			arg.OrgID, newBalance,
		)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO credit_transactions (org_id, user_id, report_id, amount, balance, description)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			arg.OrgID, arg.UserID, arg.ReportID, arg.Amount, newBalance, arg.Description,
		)
		return err
	})
}

// ---- Refresh Token types ----

type RefreshToken struct {
	ID        uuid.UUID `json:"id"`
	UserID    int64     `json:"user_id"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateRefreshTokenParams struct {
	UserID    int64
	Token     string // raw token — we hash before storing
	ExpiresAt time.Time
}

func (s *Store) CreateRefreshToken(ctx context.Context, arg CreateRefreshTokenParams) (RefreshToken, error) {
	hash := hashToken(arg.Token)

	var rt RefreshToken
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)
		 RETURNING id, user_id, token_hash, expires_at, revoked, created_at`,
		arg.UserID, hash, arg.ExpiresAt,
	)
	err := row.Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &rt.ExpiresAt, &rt.Revoked, &rt.CreatedAt)
	return rt, err
}

func (s *Store) GetRefreshToken(ctx context.Context, token string) (RefreshToken, error) {
	hash := hashToken(token)

	var rt RefreshToken
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at, revoked, created_at
		 FROM refresh_tokens WHERE token_hash = $1`,
		hash,
	)
	err := row.Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &rt.ExpiresAt, &rt.Revoked, &rt.CreatedAt)
	return rt, err
}

func (s *Store) RevokeRefreshToken(ctx context.Context, token string) error {
	hash := hashToken(token)
	_, err := s.db.ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`,
		hash,
	)
	return err
}

// hashToken returns a SHA-256 hex hash of the token
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", h)
}

// slugify converts a name to a URL-safe slug
func Slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	// Remove characters that are not alphanumeric or hyphen
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}
	return strings.Trim(result.String(), "-")
}

// ---- Admin (super_admin) queries ----

type OrgWithStats struct {
	ID               int64          `json:"id"`
	Name             string         `json:"name"`
	Slug             string         `json:"slug"`
	LogoURL          sql.NullString `json:"logo_url"`
	Plan             string         `json:"plan"`
	Credits          int32          `json:"credits"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	CompetitorsCount int64          `json:"competitors_count"`
	ReportsCount     int64          `json:"reports_count"`
	UsersCount       int64          `json:"users_count"`
}

func (s *Store) ListAllOrganizations(ctx context.Context) ([]OrgWithStats, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT o.id, o.name, o.slug, o.logo_url, o.plan, o.credits, o.created_at, o.updated_at,
		        COUNT(DISTINCT c.id) AS competitors_count,
		        COUNT(DISTINCT r.id) AS reports_count,
		        COUNT(DISTINCT u.id) AS users_count
		 FROM organizations o
		 LEFT JOIN competitors c ON c.org_id = o.id
		 LEFT JOIN reports r ON r.org_id = o.id
		 LEFT JOIN users u ON u.org_id = o.id
		 GROUP BY o.id
		 ORDER BY o.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orgs []OrgWithStats
	for rows.Next() {
		var o OrgWithStats
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.LogoURL, &o.Plan, &o.Credits,
			&o.CreatedAt, &o.UpdatedAt, &o.CompetitorsCount, &o.ReportsCount, &o.UsersCount); err != nil {
			return nil, err
		}
		orgs = append(orgs, o)
	}
	return orgs, rows.Err()
}

type PlatformStats struct {
	TotalOrgs        int64 `json:"total_orgs"`
	TotalUsers       int64 `json:"total_users"`
	TotalCompetitors int64 `json:"total_competitors"`
	TotalReports     int64 `json:"total_reports"`
	TotalCredits     int64 `json:"total_credits"`
}

func (s *Store) GetPlatformStats(ctx context.Context) (PlatformStats, error) {
	var ps PlatformStats
	row := s.db.QueryRowContext(ctx,
		`SELECT
			(SELECT COUNT(*) FROM organizations),
			(SELECT COUNT(*) FROM users),
			(SELECT COUNT(*) FROM competitors),
			(SELECT COUNT(*) FROM reports),
			(SELECT COALESCE(SUM(credits), 0) FROM organizations)`)
	err := row.Scan(&ps.TotalOrgs, &ps.TotalUsers, &ps.TotalCompetitors, &ps.TotalReports, &ps.TotalCredits)
	return ps, err
}

type UpdateOrgParams struct {
	ID      int64
	Plan    string
	Credits int32
}

func (s *Store) UpdateOrganizationPlanAndCredits(ctx context.Context, arg UpdateOrgParams) (Organization, error) {
	var org Organization
	row := s.db.QueryRowContext(ctx,
		`UPDATE organizations SET plan = $2, credits = $3, updated_at = NOW() WHERE id = $1
		 RETURNING id, name, slug, logo_url, plan, credits, created_at, updated_at`,
		arg.ID, arg.Plan, arg.Credits,
	)
	err := row.Scan(&org.ID, &org.Name, &org.Slug, &org.LogoURL, &org.Plan, &org.Credits, &org.CreatedAt, &org.UpdatedAt)
	return org, err
}

func (s *Store) DeleteOrganization(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM organizations WHERE id = $1`, id)
	return err
}

// ---- Operation Costs ----

type OperationCost struct {
	ID          int64     `json:"id"`
	Operation   string    `json:"operation"`
	CreditCost  int32     `json:"credit_cost"`
	IsEnabled   bool      `json:"is_enabled"`
	Description string    `json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Store) ListOperationCosts(ctx context.Context) ([]OperationCost, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, operation, credit_cost, is_enabled, COALESCE(description,''), updated_at FROM operation_costs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []OperationCost
	for rows.Next() {
		var oc OperationCost
		if err := rows.Scan(&oc.ID, &oc.Operation, &oc.CreditCost, &oc.IsEnabled, &oc.Description, &oc.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, oc)
	}
	return result, rows.Err()
}

func (s *Store) GetOperationCost(ctx context.Context, operation string) (int32, error) {
	var cost int32
	err := s.db.QueryRowContext(ctx,
		`SELECT credit_cost FROM operation_costs WHERE operation = $1 AND is_enabled = TRUE`, operation).Scan(&cost)
	return cost, err
}

func (s *Store) UpdateOperationCost(ctx context.Context, operation string, creditCost int32) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE operation_costs SET credit_cost = $2, updated_at = NOW() WHERE operation = $1`,
		operation, creditCost)
	return err
}

// ---- Usage Log ----

type UsageLog struct {
	ID           int64           `json:"id"`
	OrgID        int64           `json:"org_id"`
	UserID       *int64          `json:"user_id"`
	Operation    string          `json:"operation"`
	CreditCost   int32           `json:"credit_cost"`
	Status       string          `json:"status"`
	CompetitorID *int64          `json:"competitor_id"`
	ReportID     *int64          `json:"report_id"`
	Metadata     json.RawMessage `json:"metadata"`
	CreatedAt    time.Time       `json:"created_at"`
}

type InsertUsageLogParams struct {
	OrgID        int64
	UserID       *int64
	Operation    string
	CreditCost   int32
	Status       string
	CompetitorID *int64
	ReportID     *int64
	Metadata     json.RawMessage
}

func (s *Store) InsertUsageLog(ctx context.Context, arg InsertUsageLogParams) (int64, error) {
	meta := arg.Metadata
	if meta == nil {
		meta = json.RawMessage(`{}`)
	}
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO usage_log (org_id, user_id, operation, credit_cost, status, competitor_id, report_id, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		arg.OrgID, arg.UserID, arg.Operation, arg.CreditCost, arg.Status, arg.CompetitorID, arg.ReportID, meta,
	).Scan(&id)
	return id, err
}

func (s *Store) UpdateUsageLogStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE usage_log SET status = $2 WHERE id = $1`, id, status)
	return err
}

func (s *Store) UpdateUsageLogMetadata(ctx context.Context, id int64, metadata json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, `UPDATE usage_log SET metadata = $2 WHERE id = $1`, id, metadata)
	return err
}

type OrgUsageSummary struct {
	Operation   string `json:"operation"`
	TotalOps    int64  `json:"total_ops"`
	TotalCost   int64  `json:"total_cost"`
}

func (s *Store) GetOrgUsageSummary(ctx context.Context, orgID int64, daysBack int) ([]OrgUsageSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT operation, COUNT(*) as total_ops, COALESCE(SUM(credit_cost), 0) as total_cost
		 FROM usage_log
		 WHERE org_id = $1 AND status = 'completed' AND created_at >= NOW() - INTERVAL '1 day' * $2
		 GROUP BY operation ORDER BY total_cost DESC`,
		orgID, daysBack)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []OrgUsageSummary
	for rows.Next() {
		var s OrgUsageSummary
		if err := rows.Scan(&s.Operation, &s.TotalOps, &s.TotalCost); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (s *Store) GetOrgUsageTotal(ctx context.Context, orgID int64, daysBack int) (int64, error) {
	var total int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(credit_cost), 0) FROM usage_log
		 WHERE org_id = $1 AND status = 'completed' AND created_at >= NOW() - INTERVAL '1 day' * $2`,
		orgID, daysBack).Scan(&total)
	return total, err
}

// DeductCreditsAtomic atomically deducts credits and returns new balance. Returns error if insufficient.
func (s *Store) DeductCreditsAtomic(ctx context.Context, orgID int64, amount int32) (int32, error) {
	var newBalance int32
	err := s.execTx(ctx, func(tx *sql.Tx) error {
		var credits int32
		if err := tx.QueryRowContext(ctx,
			`SELECT credits FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&credits); err != nil {
			return err
		}
		if credits < amount {
			return fmt.Errorf("insufficient credits: have %d, need %d", credits, amount)
		}
		newBalance = credits - amount
		_, err := tx.ExecContext(ctx,
			`UPDATE organizations SET credits = $2, updated_at = NOW() WHERE id = $1`, orgID, newBalance)
		return err
	})
	return newBalance, err
}

func (s *Store) RefundCredits(ctx context.Context, orgID int64, amount int32) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE organizations SET credits = credits + $2, updated_at = NOW() WHERE id = $1`, orgID, amount)
	return err
}

// ---- Plan Limits ----

type PlanLimit struct {
	ID              int64  `json:"id"`
	Plan            string `json:"plan"`
	MonthlyCredits  int32  `json:"monthly_credits"`
	MaxCompetitors  int32  `json:"max_competitors"`
	MaxReportsMonth int32  `json:"max_reports_month"`
}

func (s *Store) ListPlanLimits(ctx context.Context) ([]PlanLimit, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, plan, monthly_credits, max_competitors, max_reports_month FROM plan_limits ORDER BY monthly_credits ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []PlanLimit
	for rows.Next() {
		var pl PlanLimit
		if err := rows.Scan(&pl.ID, &pl.Plan, &pl.MonthlyCredits, &pl.MaxCompetitors, &pl.MaxReportsMonth); err != nil {
			return nil, err
		}
		result = append(result, pl)
	}
	return result, rows.Err()
}

func (s *Store) GetPlanLimit(ctx context.Context, plan string) (PlanLimit, error) {
	var pl PlanLimit
	err := s.db.QueryRowContext(ctx,
		`SELECT id, plan, monthly_credits, max_competitors, max_reports_month FROM plan_limits WHERE plan = $1`, plan).
		Scan(&pl.ID, &pl.Plan, &pl.MonthlyCredits, &pl.MaxCompetitors, &pl.MaxReportsMonth)
	return pl, err
}

func (s *Store) UpdatePlanLimit(ctx context.Context, plan string, monthlyCredits, maxCompetitors, maxReportsMonth int32) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE plan_limits SET monthly_credits = $2, max_competitors = $3, max_reports_month = $4, updated_at = NOW() WHERE plan = $1`,
		plan, monthlyCredits, maxCompetitors, maxReportsMonth)
	return err
}

func (s *Store) CountCompetitorsByOrg(ctx context.Context, orgID int64) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM competitors WHERE org_id = $1`, orgID).Scan(&count)
	return count, err
}

// ListStaleCompetitors returns competitors whose last_scraped_at is older than the given duration
// (or has never been scraped). Only returns those NOT currently being scraped.
// Used by the scheduler and lazy refresh logic.
func (s *Store) ListStaleCompetitors(ctx context.Context, staleThresholdHours int) ([]Competitor, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, platform, username, display_name, profile_pic_url, is_own_account, scrape_status, scrape_error, last_scraped_at, created_at, updated_at
		 FROM competitors
		 WHERE scrape_status != 'scraping'
		   AND (last_scraped_at IS NULL OR last_scraped_at < NOW() - INTERVAL '1 hour' * $1)
		 ORDER BY last_scraped_at ASC NULLS FIRST
		 LIMIT 100`,
		staleThresholdHours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Competitor
	for rows.Next() {
		var c Competitor
		if err := rows.Scan(&c.ID, &c.OrgID, &c.Platform, &c.Username, &c.DisplayName, &c.ProfilePicURL,
			&c.IsOwnAccount, &c.ScrapeStatus, &c.ScrapeError, &c.LastScrapedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// ListStaleCompetitorsByOrg returns stale competitors for a specific org (for lazy refresh).
func (s *Store) ListStaleCompetitorsByOrg(ctx context.Context, orgID int64, staleThresholdHours int) ([]Competitor, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, platform, username, display_name, profile_pic_url, is_own_account, scrape_status, scrape_error, last_scraped_at, created_at, updated_at
		 FROM competitors
		 WHERE org_id = $1
		   AND scrape_status != 'scraping'
		   AND (last_scraped_at IS NULL OR last_scraped_at < NOW() - INTERVAL '1 hour' * $2)`,
		orgID, staleThresholdHours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []Competitor
	for rows.Next() {
		var c Competitor
		if err := rows.Scan(&c.ID, &c.OrgID, &c.Platform, &c.Username, &c.DisplayName, &c.ProfilePicURL,
			&c.IsOwnAccount, &c.ScrapeStatus, &c.ScrapeError, &c.LastScrapedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// ---- Tracked keywords ----

type TrackedKeyword struct {
	ID        int64     `json:"id"`
	OrgID     int64     `json:"org_id"`
	Keyword   string    `json:"keyword"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) ListTrackedKeywords(ctx context.Context, orgID int64) ([]TrackedKeyword, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, keyword, created_at
		 FROM tracked_keywords WHERE org_id = $1 ORDER BY created_at ASC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrackedKeyword
	for rows.Next() {
		var k TrackedKeyword
		if err := rows.Scan(&k.ID, &k.OrgID, &k.Keyword, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) CreateTrackedKeyword(ctx context.Context, orgID int64, keyword string) (TrackedKeyword, error) {
	var k TrackedKeyword
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO tracked_keywords (org_id, keyword) VALUES ($1, $2)
		 RETURNING id, org_id, keyword, created_at`,
		orgID, keyword,
	).Scan(&k.ID, &k.OrgID, &k.Keyword, &k.CreatedAt)
	return k, err
}

func (s *Store) DeleteTrackedKeyword(ctx context.Context, id int64, orgID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM tracked_keywords WHERE id = $1 AND org_id = $2`,
		id, orgID,
	)
	return err
}

func (s *Store) GetTrackedKeyword(ctx context.Context, id int64, orgID int64) (TrackedKeyword, error) {
	var k TrackedKeyword
	err := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, keyword, created_at
		 FROM tracked_keywords WHERE id = $1 AND org_id = $2`,
		id, orgID,
	).Scan(&k.ID, &k.OrgID, &k.Keyword, &k.CreatedAt)
	return k, err
}
