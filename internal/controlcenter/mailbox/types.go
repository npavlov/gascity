// Package mailbox projects the configured city's read-only Supervisor mail
// endpoints into the Control Center contract.
package mailbox

import (
	"context"
	"time"
)

// Filter is the closed Supervisor inbox filter surface.
type Filter string

const (
	FilterUnread Filter = "unread"
	FilterAll    Filter = "all"
)

// Query selects one bounded upstream inbox page.
type Query struct {
	Filter Filter
	Cursor string
	Limit  int
}

// Message is the generated-client-independent mail message contract.
type Message struct {
	ID        string    `json:"id" minLength:"1"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	Read      bool      `json:"read"`
	ThreadID  *string   `json:"thread_id,omitempty"`
	ReplyTo   *string   `json:"reply_to,omitempty"`
	Priority  *int64    `json:"priority,omitempty"`
	CC        []string  `json:"cc"`
	Rig       *string   `json:"rig,omitempty"`
}

// Count is the city-global mailbox count snapshot.
type Count struct {
	Total         int64    `json:"total" minimum:"0"`
	Unread        int64    `json:"unread" minimum:"0"`
	Partial       bool     `json:"partial"`
	PartialErrors []string `json:"partial_errors"`
}

// Page is exactly one bounded upstream inbox page.
type Page struct {
	Items         []Message `json:"items"`
	Total         int64     `json:"total" minimum:"0"`
	NextCursor    string    `json:"next_cursor,omitempty"`
	Partial       bool      `json:"partial"`
	PartialErrors []string  `json:"partial_errors"`
}

// Thread is one ordered upstream thread snapshot.
type Thread struct {
	Items         []Message `json:"items"`
	Total         int64     `json:"total" minimum:"0"`
	Partial       bool      `json:"partial"`
	Truncated     bool      `json:"truncated"`
	PartialErrors []string  `json:"partial_errors"`
}

// Reader is the complete read-only local mailbox boundary.
type Reader interface {
	Count(context.Context) (Count, error)
	List(context.Context, Query) (Page, error)
	Get(context.Context, string, *string) (Message, error)
	Thread(context.Context, string, *string) (Thread, error)
}
