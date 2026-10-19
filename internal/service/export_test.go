package service

import (
	"context"
	"time"

	"bshop/internal/store"
)

// SetHealthRetryPause shortens the pause between health check attempts in tests.
func SetHealthRetryPause(d time.Duration) { healthRetryPause = d }

// EmitEventForTest raises an admin event as the service code does.
func EmitEventForTest(s *Service, ctx context.Context, key string, c *store.Customer, vars map[string]any) {
	s.event(ctx, key, c, vars)
}
