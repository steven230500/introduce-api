// Package presentation owns what is on the projector right now, and the
// websocket hub that keeps every window agreeing on it.
package presentation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// State is one operator's live position in a service.
type State struct {
	CollectionID      *uuid.UUID `json:"collection_id"`
	CurrentItemIndex  int        `json:"current_item_index"`
	CurrentSlideIndex int        `json:"current_slide_index"`
	IsLive            bool       `json:"is_live"`
	BlankScreen       bool       `json:"blank_screen"`
	CountdownActive   bool       `json:"countdown_active"`
	CountdownEnd      *time.Time `json:"countdown_end"`
	OverlayVisible    bool       `json:"overlay_visible"`
	OverlayText       *string    `json:"overlay_text"`

	// StageMessage reaches the stage monitor and nothing else, so the platform
	// can be told something the congregation is not.
	StageMessage *string `json:"stage_message"`

	// Waiting is the scene shown while nothing is on the screen: which one,
	// whether it is up, and the text over it. Carried as the client sent it,
	// because the server has no opinion about scenes and a new setting on one
	// should not need a server release to reach the projector.
	Waiting json.RawMessage `json:"waiting,omitempty"`

	// Timing is when the item on the screen started, the plan it is counted
	// against, and whether this is a rehearsal. Opaque here for the same
	// reason as Waiting.
	Timing json.RawMessage `json:"timing,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`
}

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Get returns the stored state, or a zero state when the operator has never
// projected anything. A window that connects mid-service reads this first, so
// it shows the right slide instead of a black screen until the next change.
func (r *Repo) Get(ctx context.Context, userID uuid.UUID) (State, error) {
	var s State
	err := r.pool.QueryRow(ctx, `
		select collection_id, current_item_index, current_slide_index, is_live,
		       blank_screen, countdown_active, countdown_end, overlay_visible,
		       overlay_text, stage_message, waiting, timing, updated_at
		from presentation_state where user_id = $1`, userID,
	).Scan(&s.CollectionID, &s.CurrentItemIndex, &s.CurrentSlideIndex, &s.IsLive,
		&s.BlankScreen, &s.CountdownActive, &s.CountdownEnd, &s.OverlayVisible,
		&s.OverlayText, &s.StageMessage, &s.Waiting, &s.Timing, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return State{UpdatedAt: time.Now()}, nil
	}
	return s, err
}

func (r *Repo) Upsert(ctx context.Context, userID uuid.UUID, s State) (State, error) {
	err := r.pool.QueryRow(ctx, `
		insert into presentation_state (
			user_id, collection_id, current_item_index, current_slide_index,
			is_live, blank_screen, countdown_active, countdown_end,
			overlay_visible, overlay_text, stage_message, waiting, timing
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		on conflict (user_id) do update set
			collection_id       = excluded.collection_id,
			current_item_index  = excluded.current_item_index,
			current_slide_index = excluded.current_slide_index,
			is_live             = excluded.is_live,
			blank_screen        = excluded.blank_screen,
			countdown_active    = excluded.countdown_active,
			countdown_end       = excluded.countdown_end,
			overlay_visible     = excluded.overlay_visible,
			overlay_text        = excluded.overlay_text,
			stage_message       = excluded.stage_message,
			waiting             = excluded.waiting,
			timing              = excluded.timing
		returning updated_at`,
		userID, s.CollectionID, s.CurrentItemIndex, s.CurrentSlideIndex,
		s.IsLive, s.BlankScreen, s.CountdownActive, s.CountdownEnd,
		s.OverlayVisible, s.OverlayText, s.StageMessage, nullableJSON(s.Waiting),
		nullableJSON(s.Timing),
	).Scan(&s.UpdatedAt)
	return s, err
}

// nullableJSON stores an absent waiting screen as SQL null rather than the
// literal JSON null, so "never set" and "set to nothing" read the same.
func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return []byte(raw)
}
