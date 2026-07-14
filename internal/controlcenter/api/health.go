package controlapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// HealthBody is the versioned local application health projection.
type HealthBody struct {
	SchemaVersion       int    `json:"schema_version" doc:"Health response schema version" minimum:"1"`
	Status              string `json:"status" doc:"Local application status" enum:"ok,degraded"`
	City                string `json:"city" doc:"Configured GasCity city"`
	SupervisorReachable bool   `json:"supervisor_reachable" doc:"Whether the Supervisor returned typed health"`
}

type healthOutput struct {
	Body HealthBody
}

func registerHealth(api huma.API, opts Options) {
	huma.Get(api, "/api/v1/health", func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		reachable := opts.SupervisorPing != nil && opts.SupervisorPing(ctx) == nil
		status := "ok"
		if !reachable {
			status = "degraded"
		}
		return &healthOutput{Body: HealthBody{
			SchemaVersion:       1,
			Status:              status,
			City:                opts.CityName,
			SupervisorReachable: reachable,
		}}, nil
	})
}
