package controlapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/controlcenter/mailbox"
)

const mailSchemaVersion = 1

// MailMessage is the versioned wire projection for one mailbox row/detail.
type MailMessage struct {
	SchemaVersion int       `json:"schema_version" minimum:"1"`
	ID            string    `json:"id" minLength:"1"`
	From          string    `json:"from"`
	To            string    `json:"to"`
	Subject       string    `json:"subject"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at"`
	Read          bool      `json:"read"`
	ThreadID      *string   `json:"thread_id,omitempty"`
	ReplyTo       *string   `json:"reply_to,omitempty"`
	Priority      *int64    `json:"priority,omitempty"`
	CC            []string  `json:"cc"`
	Rig           *string   `json:"rig,omitempty"`
}

// MailCount is the versioned city-global unread/total snapshot.
type MailCount struct {
	SchemaVersion int      `json:"schema_version" minimum:"1"`
	Total         int64    `json:"total" minimum:"0"`
	Unread        int64    `json:"unread" minimum:"0"`
	Partial       bool     `json:"partial"`
	PartialErrors []string `json:"partial_errors"`
}

// MailPage is one versioned, bounded inbox page.
type MailPage struct {
	SchemaVersion int           `json:"schema_version" minimum:"1"`
	Items         []MailMessage `json:"items"`
	Total         int64         `json:"total" minimum:"0"`
	NextCursor    string        `json:"next_cursor,omitempty"`
	Partial       bool          `json:"partial"`
	PartialErrors []string      `json:"partial_errors"`
}

// MailThread is one versioned ordered thread snapshot.
type MailThread struct {
	SchemaVersion int           `json:"schema_version" minimum:"1"`
	Items         []MailMessage `json:"items"`
	Total         int64         `json:"total" minimum:"0"`
	Partial       bool          `json:"partial"`
	Truncated     bool          `json:"truncated"`
	PartialErrors []string      `json:"partial_errors"`
}

type mailCountOutput struct {
	Body MailCount
}

type mailListInput struct {
	Status string `query:"status" default:"unread" enum:"unread,all" doc:"Inbox filter."`
	Cursor string `query:"cursor" maxLength:"1024" doc:"Opaque Supervisor page cursor."`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100" doc:"Maximum messages in this page."`
}

func (input *mailListInput) Resolve(ctx huma.Context) []error {
	u := ctx.URL()
	query := u.Query()
	validationErrors := make([]error, 0, 3)
	if query.Has("status") && query.Get("status") == "" {
		validationErrors = append(validationErrors, &huma.ErrorDetail{Location: "query.status", Message: "must not be empty when supplied"})
	}
	if query.Has("limit") && query.Get("limit") == "" {
		validationErrors = append(validationErrors, &huma.ErrorDetail{Location: "query.limit", Message: "must not be empty when supplied"})
	}
	if query.Has("cursor") && input.Cursor == "" {
		validationErrors = append(validationErrors, &huma.ErrorDetail{Location: "query.cursor", Message: "must not be empty when supplied"})
	}
	return validationErrors
}

type mailPageOutput struct {
	Body MailPage
}

type mailLookupInput struct {
	ID  string `path:"id" minLength:"1" maxLength:"128" pattern:"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$" doc:"Mail message or thread ID."`
	Rig string `query:"rig" maxLength:"128" doc:"Opaque provider lookup hint."`
}

func (input *mailLookupInput) Resolve(ctx huma.Context) []error {
	u := ctx.URL()
	if u.Query().Has("rig") && input.Rig == "" {
		return []error{&huma.ErrorDetail{Location: "query.rig", Message: "must not be empty when supplied"}}
	}
	return nil
}

type mailMessageOutput struct {
	Body MailMessage
}

type mailThreadOutput struct {
	Body MailThread
}

func registerMail(api huma.API, opts Options) {
	huma.Get(api, "/api/v1/mail/count", func(ctx context.Context, _ *struct{}) (*mailCountOutput, error) {
		if opts.Mail == nil {
			return nil, mailUnavailable()
		}
		count, err := opts.Mail.Count(ctx)
		if err != nil {
			return nil, mapMailError(err)
		}
		return &mailCountOutput{Body: projectMailCount(count)}, nil
	})

	huma.Get(api, "/api/v1/mail", func(ctx context.Context, input *mailListInput) (*mailPageOutput, error) {
		if opts.Mail == nil {
			return nil, mailUnavailable()
		}
		query := mailbox.Query{Filter: mailbox.Filter(input.Status), Limit: input.Limit}
		query.Cursor = input.Cursor
		page, err := opts.Mail.List(ctx, query)
		if err != nil {
			return nil, mapMailError(err)
		}
		return &mailPageOutput{Body: projectMailPage(page)}, nil
	})

	huma.Get(api, "/api/v1/mail/{id}", func(ctx context.Context, input *mailLookupInput) (*mailMessageOutput, error) {
		if opts.Mail == nil {
			return nil, mailUnavailable()
		}
		message, err := opts.Mail.Get(ctx, input.ID, optionalMailHint(input.Rig))
		if err != nil {
			return nil, mapMailError(err)
		}
		return &mailMessageOutput{Body: projectMailMessage(message)}, nil
	})

	huma.Get(api, "/api/v1/mail/{id}/thread", func(ctx context.Context, input *mailLookupInput) (*mailThreadOutput, error) {
		if opts.Mail == nil {
			return nil, mailUnavailable()
		}
		thread, err := opts.Mail.Thread(ctx, input.ID, optionalMailHint(input.Rig))
		if err != nil {
			return nil, mapMailError(err)
		}
		return &mailThreadOutput{Body: projectMailThread(thread)}, nil
	})
}

func optionalMailHint(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func projectMailMessage(message mailbox.Message) MailMessage {
	return MailMessage{
		SchemaVersion: mailSchemaVersion,
		ID:            message.ID,
		From:          message.From,
		To:            message.To,
		Subject:       message.Subject,
		Body:          message.Body,
		CreatedAt:     message.CreatedAt,
		Read:          message.Read,
		ThreadID:      message.ThreadID,
		ReplyTo:       message.ReplyTo,
		Priority:      message.Priority,
		CC:            append([]string{}, message.CC...),
		Rig:           message.Rig,
	}
}

func projectMailMessages(messages []mailbox.Message) []MailMessage {
	result := make([]MailMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, projectMailMessage(message))
	}
	return result
}

func projectMailCount(count mailbox.Count) MailCount {
	return MailCount{SchemaVersion: mailSchemaVersion, Total: count.Total, Unread: count.Unread, Partial: count.Partial, PartialErrors: append([]string{}, count.PartialErrors...)}
}

func projectMailPage(page mailbox.Page) MailPage {
	return MailPage{SchemaVersion: mailSchemaVersion, Items: projectMailMessages(page.Items), Total: page.Total, NextCursor: page.NextCursor, Partial: page.Partial, PartialErrors: append([]string{}, page.PartialErrors...)}
}

func projectMailThread(thread mailbox.Thread) MailThread {
	return MailThread{SchemaVersion: mailSchemaVersion, Items: projectMailMessages(thread.Items), Total: thread.Total, Partial: thread.Partial, Truncated: thread.Truncated, PartialErrors: append([]string{}, thread.PartialErrors...)}
}

func mailUnavailable() error {
	return huma.Error503ServiceUnavailable("Control Center Supervisor mail is unavailable")
}

func mapMailError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return huma.Error503ServiceUnavailable(err.Error())
	}
	var upstream *mailbox.UpstreamError
	if errors.As(err, &upstream) {
		if upstream.StatusCode == http.StatusNotFound {
			return huma.Error404NotFound(upstream.Detail)
		}
		if upstream.Code == "upstream_unavailable" || upstream.StatusCode == http.StatusServiceUnavailable || upstream.StatusCode == http.StatusGatewayTimeout {
			return huma.Error503ServiceUnavailable(upstream.Detail)
		}
		if upstream.Code == "upstream_protocol" || upstream.Code == "upstream_http" {
			return huma.Error502BadGateway(upstream.Detail)
		}
	}
	return huma.Error503ServiceUnavailable(err.Error())
}
