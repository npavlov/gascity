package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
)

const eventKeepaliveInterval = 15 * time.Second

func registerEvents(api huma.API, opts Options) {
	keepalive := eventKeepaliveInterval
	if opts.eventKeepalive > 0 {
		keepalive = opts.eventKeepalive
	}
	dataSchema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(gcstate.Invalidation{}), true, "Invalidation")
	op := huma.Operation{
		Method:      http.MethodGet,
		Path:        "/api/v1/events",
		OperationID: "streamControlCenterEvents",
		Summary:     "Stream authoritative-state invalidations",
		Description: "Relays bounded invalidation hints. Clients re-read the affected authoritative resources.",
		Errors:      []int{http.StatusServiceUnavailable},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "Server-sent invalidation events.",
				Content: map[string]*huma.MediaType{
					"text/event-stream": {
						Schema: &huma.Schema{
							Title:       "Control Center invalidation events",
							Description: "A stream of invalidate events.",
							Type:        huma.TypeArray,
							Items: &huma.Schema{
								Type: huma.TypeObject,
								Properties: map[string]*huma.Schema{
									"event": {Type: huma.TypeString, Extensions: map[string]any{"const": "invalidate"}},
									"id":    {Type: huma.TypeString},
									"data":  dataSchema,
								},
								Required: []string{"event", "id", "data"},
							},
						},
					},
				},
			},
		},
	}

	huma.Register(api, op, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		if opts.Events == nil {
			return nil, huma.Error503ServiceUnavailable("Control Center event hub is unavailable")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			streamLocalEvents(hctx, opts.Events, keepalive)
		}}, nil
	})
}

func streamLocalEvents(hctx huma.Context, hub *gcstate.Hub, keepalive time.Duration) {
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
		invalidation, err := subscription.Next(nextCtx)
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
		payload, err := json.Marshal(invalidation)
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(writer, "event: invalidate\nid: %s\ndata: %s\n\n", invalidation.Cursor, payload); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}
