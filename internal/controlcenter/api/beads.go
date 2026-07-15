package controlapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
)

type beadListOutput struct {
	Body gcstate.ResourceList[gcstate.BeadView]
}

func registerBeads(api huma.API, opts Options) {
	huma.Get(api, "/api/v1/convoys/{id}/beads", func(ctx context.Context, input *convoyIDInput) (*beadListOutput, error) {
		if opts.State == nil {
			return nil, stateUnavailable()
		}
		beads, err := opts.State.ListBeads(ctx, input.ID)
		if err != nil {
			return nil, mapStateError(err)
		}
		return &beadListOutput{Body: beads}, nil
	})
}
