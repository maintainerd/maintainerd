package steward

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ServiceTier's second return value is the load-bearing part: a caller must be
// able to tell an explicit TierApp opt-out from catalog SILENCE, because core and
// auth are provisioned by SetupService and never appear in the catalog.
func TestCatalogServiceTier(t *testing.T) {
	cat := Catalog{Objects: []Object{
		{APIVersion, KindService, Meta{"secret"}, ServiceSpec{Tier: TierSystem}},
		{APIVersion, KindService, Meta{"reports"}, ServiceSpec{Tier: TierApp}},
		// A non-Service object with the same name must not answer for it.
		{APIVersion, KindResourceAPI, Meta{"auth"}, ResourceAPISpec{Service: "auth"}},
	}}

	tests := []struct {
		name         string
		service      string
		wantTier     Tier
		wantDeclared bool
	}{
		{"system service", "secret", TierSystem, true},
		{"app service", "reports", TierApp, true},
		{"declared only as a ResourceAPI is not a Service declaration", "auth", "", false},
		{"absent entirely", "core", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier, declared := cat.ServiceTier(tt.service)
			assert.Equal(t, tt.wantTier, tier)
			assert.Equal(t, tt.wantDeclared, declared)
		})
	}
}

func TestTierIsSystem(t *testing.T) {
	assert.True(t, TierSystem.IsSystem())
	assert.False(t, TierApp.IsSystem())
	assert.False(t, Tier("").IsSystem())
}
