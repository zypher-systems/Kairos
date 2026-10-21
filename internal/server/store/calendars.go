package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Calendar is a named set of skip days (YYYY-MM-DD).
type Calendar struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Dates     []string  `json:"dates"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const dateLayout = "2006-01-02"

// Validate normalizes name and dates.
func (c *Calendar) Validate() error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return errors.New("name is required")
	}
	seen := map[string]struct{}{}
	var dates []string
	for _, raw := range c.Dates {
		d := strings.TrimSpace(raw)
		if d == "" {
			continue
		}
		t, err := time.Parse(dateLayout, d)
		if err != nil || t.Format(dateLayout) != d {
			return fmt.Errorf("invalid date %q (want YYYY-MM-DD)", d)
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		dates = append(dates, d)
	}
	sort.Strings(dates)
	if dates == nil {
		dates = []string{}
	}
	c.Dates = dates
	return nil
}

const calendarCols = `id, name, dates, created_at, updated_at`

func scanCalendar(row pgx.Row) (Calendar, error) {
	var c Calendar
	var raw []byte
	err := row.Scan(&c.ID, &c.Name, &raw, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	c.Dates = unmarshalStringSlice(raw)
	return c, nil
}

func (s *Store) CreateCalendar(ctx context.Context, c *Calendar) error {
	if err := c.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(c.Dates)
	if err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `
		INSERT INTO calendars (name, dates)
		VALUES ($1,$2)
		RETURNING id, created_at, updated_at`,
		c.Name, raw,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

func (s *Store) GetCalendar(ctx context.Context, id uuid.UUID) (Calendar, error) {
	return scanCalendar(s.pool.QueryRow(ctx, `SELECT `+calendarCols+` FROM calendars WHERE id = $1`, id))
}

func (s *Store) ListCalendars(ctx context.Context) ([]Calendar, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+calendarCols+` FROM calendars ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Calendar
	for rows.Next() {
		c, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCalendar(ctx context.Context, c *Calendar) error {
	if err := c.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(c.Dates)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE calendars SET name=$1, dates=$2, updated_at=now()
		WHERE id=$3`,
		c.Name, raw, c.ID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteCalendar(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM calendars WHERE id = $1`, id)
	return err
}

// CalendarByName returns the unique calendar with this name.
func (s *Store) CalendarByName(ctx context.Context, name string) (Calendar, error) {
	return scanCalendar(s.pool.QueryRow(ctx, `SELECT `+calendarCols+` FROM calendars WHERE name = $1`, name))
}

// CalendarName resolves a calendar id to its name (empty if missing).
func (s *Store) CalendarName(ctx context.Context, id uuid.UUID) string {
	c, err := s.GetCalendar(ctx, id)
	if err != nil {
		return ""
	}
	return c.Name
}
