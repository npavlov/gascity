package controlapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
)

type orderListOutput struct {
	Body gcstate.ResourceList[gcstate.OrderView]
}

type orderHistoryInput struct {
	ScopedName string `query:"scoped_name" required:"true" minLength:"1" maxLength:"256" pattern:"^\\S(.*\\S)?$" doc:"Scoped order name."`
	Before     string `query:"before" format:"date-time" doc:"Exclusive RFC3339 history cursor."`
	Limit      int    `query:"limit" default:"20" minimum:"1" maximum:"20" doc:"Maximum history rows."`
}

type orderHistoryOutput struct {
	Body gcstate.ResourceList[gcstate.OrderRunView]
}

type orderOutputInput struct {
	BeadID   string `path:"bead_id" minLength:"1" maxLength:"128" pattern:"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$" doc:"Order-run bead ID."`
	StoreRef string `query:"store_ref" required:"true" minLength:"1" maxLength:"256" pattern:"^\\S(.*\\S)?$" doc:"Exact run store reference."`
}

type orderRunOutput struct {
	Body gcstate.OrderRunOutput
}

func registerOrders(api huma.API, opts Options) {
	huma.Get(api, "/api/v1/orders", func(ctx context.Context, _ *struct{}) (*orderListOutput, error) {
		if opts.State == nil {
			return nil, stateUnavailable()
		}
		orders, err := opts.State.ListOrders(ctx)
		if err != nil {
			return nil, mapStateError(err)
		}
		return &orderListOutput{Body: orders}, nil
	})
	huma.Get(api, "/api/v1/orders/history", func(ctx context.Context, input *orderHistoryInput) (*orderHistoryOutput, error) {
		if opts.State == nil {
			return nil, stateUnavailable()
		}
		runs, err := opts.State.ListOrderHistory(ctx, input.ScopedName, input.Before, input.Limit)
		if err != nil {
			return nil, mapStateError(err)
		}
		return &orderHistoryOutput{Body: runs}, nil
	})
	huma.Get(api, "/api/v1/orders/history/{bead_id}", func(ctx context.Context, input *orderOutputInput) (*orderRunOutput, error) {
		if opts.State == nil {
			return nil, stateUnavailable()
		}
		output, err := opts.State.GetOrderRunOutput(ctx, input.BeadID, input.StoreRef)
		if err != nil {
			return nil, mapStateError(err)
		}
		return &orderRunOutput{Body: output}, nil
	})
}
