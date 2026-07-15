package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/controlcenter/mayor"
)

const (
	mayorKeepaliveInterval = 15 * time.Second
	maxMayorMessageBytes   = 64 * 1024
	maxMayorRequestIDBytes = 256
	maxMayorActionBytes    = 64
	maxMayorTextBytes      = 64 * 1024
	maxMayorMetadataKeys   = 32
	maxMayorMetadataKey    = 256
	maxMayorMetadataValue  = 4096
)

type mayorViewOutput struct{ Body mayor.MayorView }

type mayorTranscriptInput struct {
	Before string `query:"before" maxLength:"256" pattern:"^[A-Za-z0-9._:-]*$" doc:"Exclusive older-page cursor."`
}

type mayorTranscriptOutput struct{ Body mayor.TranscriptPage }

type mayorMessageBody struct {
	Message string `json:"message" minLength:"1" maxLength:"65536"`
}

type mayorMessageInput struct{ Body mayorMessageBody }

type mayorMessageOutput struct{ Body mayor.MessageReceipt }

type mayorInteractionPath struct {
	RequestID string `path:"request_id" minLength:"1" maxLength:"256" pattern:"^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$"`
	Body      mayorInteractionBody
}

type mayorInteractionBody struct {
	RequestID string            `json:"request_id" minLength:"1" maxLength:"256" pattern:"^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$"`
	Action    string            `json:"action" minLength:"1" maxLength:"64"`
	Text      string            `json:"text,omitempty" maxLength:"65536"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type mayorInteractionOutput struct{ Body mayor.InteractionReceipt }

func registerMayor(api huma.API, opts Options) {
	huma.Get(api, "/api/v1/mayor", func(ctx context.Context, _ *struct{}) (*mayorViewOutput, error) {
		if opts.Mayor == nil {
			return nil, huma.Error503ServiceUnavailable("Control Center Mayor service is unavailable")
		}
		snapshot, err := opts.Mayor.Snapshot(ctx, mayor.SnapshotOptions{AllowStale: true, IncludePending: true})
		if err != nil {
			return nil, mapMayorError(err)
		}
		view := snapshot.View
		if snapshot.PendingError != nil {
			view.Degraded = true
			view.Problems = append(view.Problems, mayor.MayorProblem{Code: "pending_unavailable", Source: "pending", Detail: snapshot.PendingError.Error(), Retryable: true})
		} else {
			view.Pending = snapshot.Pending
		}
		return &mayorViewOutput{Body: view}, nil
	})

	huma.Get(api, "/api/v1/mayor/transcript", func(ctx context.Context, input *mayorTranscriptInput) (*mayorTranscriptOutput, error) {
		if opts.Mayor == nil {
			return nil, huma.Error503ServiceUnavailable("Control Center Mayor service is unavailable")
		}
		page, err := opts.Mayor.Transcript(ctx, input.Before)
		if err != nil {
			return nil, mapMayorError(err)
		}
		return &mayorTranscriptOutput{Body: page}, nil
	})

	huma.Post(api, "/api/v1/mayor/messages", func(ctx context.Context, input *mayorMessageInput) (*mayorMessageOutput, error) {
		if opts.Mayor == nil {
			return nil, huma.Error503ServiceUnavailable("Control Center Mayor service is unavailable")
		}
		if err := validateMayorMessage(input.Body.Message); err != nil {
			return nil, err
		}
		receipt, err := opts.Mayor.Submit(ctx, input.Body.Message)
		if err != nil {
			return nil, mapMayorError(err)
		}
		return &mayorMessageOutput{Body: receipt}, nil
	})

	huma.Post(api, "/api/v1/mayor/interactions/{request_id}", func(ctx context.Context, input *mayorInteractionPath) (*mayorInteractionOutput, error) {
		if opts.Mayor == nil {
			return nil, huma.Error503ServiceUnavailable("Control Center Mayor service is unavailable")
		}
		if input.RequestID != input.Body.RequestID {
			return nil, huma.Error409Conflict("the interaction request ID changed")
		}
		if err := validateMayorInteraction(input.Body); err != nil {
			return nil, err
		}
		receipt, err := opts.Mayor.Respond(ctx, input.RequestID, mayor.InteractionInput{Action: input.Body.Action, Text: input.Body.Text, Metadata: input.Body.Metadata})
		if err != nil {
			return nil, mapMayorError(err)
		}
		return &mayorInteractionOutput{Body: receipt}, nil
	})

	registerMayorEvents(api, opts)
}

func validateMayorMessage(message string) error {
	if !utf8.ValidString(message) || len(message) == 0 || len(message) > maxMayorMessageBytes {
		return huma.Error422UnprocessableEntity("Mayor message must be valid UTF-8 and between 1 and 65536 bytes")
	}
	return nil
}

func validateMayorInteraction(input mayorInteractionBody) error {
	if !utf8.ValidString(input.Action) || len(input.Action) == 0 || len(input.Action) > maxMayorActionBytes {
		return huma.Error422UnprocessableEntity("Mayor interaction action must be between 1 and 64 UTF-8 bytes")
	}
	if !utf8.ValidString(input.Text) || len(input.Text) > maxMayorTextBytes {
		return huma.Error422UnprocessableEntity("Mayor interaction text must be valid UTF-8 and at most 65536 bytes")
	}
	if len(input.RequestID) == 0 || len(input.RequestID) > maxMayorRequestIDBytes {
		return huma.Error422UnprocessableEntity("Mayor interaction request ID must be between 1 and 256 bytes")
	}
	if len(input.Metadata) > maxMayorMetadataKeys {
		return huma.Error422UnprocessableEntity("Mayor interaction metadata may contain at most 32 keys")
	}
	for key, value := range input.Metadata {
		if !utf8.ValidString(key) || !utf8.ValidString(value) || len(key) == 0 || len(key) > maxMayorMetadataKey || len(value) > maxMayorMetadataValue {
			return huma.Error422UnprocessableEntity("Mayor interaction metadata exceeds key or value limits")
		}
	}
	return nil
}

func registerMayorEvents(api huma.API, opts Options) {
	keepalive := mayorKeepaliveInterval
	if opts.mayorKeepalive > 0 {
		keepalive = opts.mayorKeepalive
	}
	dataSchema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(mayor.MayorEvent{}), true, "MayorEvent")
	op := huma.Operation{
		Method: http.MethodGet, Path: "/api/v1/mayor/events", OperationID: "streamControlCenterMayorEvents",
		Summary: "Stream configured Mayor workspace events",
		Errors:  []int{http.StatusServiceUnavailable},
		Responses: map[string]*huma.Response{"200": {Description: "Server-sent Mayor events.", Content: map[string]*huma.MediaType{"text/event-stream": {Schema: &huma.Schema{
			Type: huma.TypeArray,
			Items: &huma.Schema{Type: huma.TypeObject, Properties: map[string]*huma.Schema{
				"event": {Type: huma.TypeString, Extensions: map[string]any{"const": "mayor"}},
				"id":    {Type: huma.TypeString},
				"data":  dataSchema,
			}, Required: []string{"event", "id", "data"}},
		}}}}},
	}
	huma.Register(api, op, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		if opts.MayorEvents == nil {
			return nil, huma.Error503ServiceUnavailable("Control Center Mayor event hub is unavailable")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) { streamMayorEvents(hctx, opts.MayorEvents, keepalive) }}, nil
	})
}

func streamMayorEvents(hctx huma.Context, hub *mayor.Hub, keepalive time.Duration) {
	subscription := hub.Subscribe()
	defer subscription.Close()
	hctx.SetHeader("Content-Type", "text/event-stream")
	hctx.SetHeader("Cache-Control", "no-cache")
	hctx.SetHeader("Connection", "keep-alive")
	writer := hctx.BodyWriter()
	flusher, _ := writer.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	for {
		nextCtx, cancel := context.WithTimeout(hctx.Context(), keepalive)
		event, err := subscription.Next(nextCtx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) && hctx.Context().Err() == nil {
			if _, writeErr := io.WriteString(writer, ": keepalive\n\n"); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			continue
		}
		if err != nil {
			return
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(writer, "event: mayor\nid: %s\ndata: %s\n\n", strings.ReplaceAll(event.Cursor, "\n", ""), payload); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}
