package controlapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
)

type convoyListOutput struct {
	Body gcstate.ResourceList[gcstate.ConvoySummary]
}

type convoyIDInput struct {
	ID string `path:"id" minLength:"1" maxLength:"128" pattern:"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$" doc:"Tracking convoy bead ID."`
}

type convoyDetailOutput struct {
	Body gcstate.ConvoyDetail
}

func registerConvoys(api huma.API, opts Options) {
	huma.Get(api, "/api/v1/convoys", func(ctx context.Context, _ *struct{}) (*convoyListOutput, error) {
		if opts.State == nil {
			return nil, stateUnavailable()
		}
		convoys, err := opts.State.ListConvoys(ctx)
		if err != nil {
			return nil, mapStateError(err)
		}
		return &convoyListOutput{Body: convoys}, nil
	})
	huma.Get(api, "/api/v1/convoys/{id}", func(ctx context.Context, input *convoyIDInput) (*convoyDetailOutput, error) {
		if opts.State == nil {
			return nil, stateUnavailable()
		}
		detail, err := opts.State.GetConvoy(ctx, input.ID)
		if err != nil {
			return nil, mapStateError(err)
		}
		return &convoyDetailOutput{Body: detail}, nil
	})
}
