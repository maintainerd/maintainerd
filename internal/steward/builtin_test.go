package steward

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mapAudienceResolver map[string]string

func (m mapAudienceResolver) AudienceFor(service string) string {
	if aud := m[service]; aud != "" {
		return aud
	}
	return "aud:" + service
}

func TestBuiltinCatalogUsesCurrentRuntimeVocabulary(t *testing.T) {
	cat := BuiltinCatalog(mapAudienceResolver{
		"secret":  "https://secret.internal",
		"runtime": "https://runtime.internal",
		"agent":   "https://agent.internal",
	})

	var serviceNames []string
	var actions []string
	var audiences []string
	for _, obj := range cat.Objects {
		switch spec := obj.Spec.(type) {
		case ServiceSpec:
			serviceNames = append(serviceNames, obj.Metadata.Name)
		case ResourceAPISpec:
			audiences = append(audiences, spec.Identifier)
			for _, perm := range spec.Permissions {
				actions = append(actions, perm.Name)
			}
		case ServicePolicySpec:
			actions = append(actions, spec.AllowedActions...)
		case ServiceClientSpec:
			audiences = append(audiences, spec.Audience)
		}
	}

	assert.Contains(t, serviceNames, "runtime")
	assert.NotContains(t, serviceNames, "docker")
	assert.Contains(t, audiences, "https://runtime.internal")
	for _, action := range actions {
		assert.False(t, strings.HasPrefix(action, "docker:"), "legacy docker action survived: %s", action)
	}
	assert.Contains(t, actions, "runtime:Run")
	assert.Contains(t, actions, "runtime:ReadLogs")
	assert.Contains(t, actions, "runtime:Exec")
	assert.Contains(t, actions, "secret:GetSecret")
}

func TestBuiltinCatalogObjectsAreInternallyConsistent(t *testing.T) {
	cat := BuiltinCatalog(mapAudienceResolver{})
	require.NotEmpty(t, cat.Objects)

	names := map[Kind]map[string]bool{}
	for _, obj := range cat.Objects {
		require.Equal(t, APIVersion, obj.APIVersion)
		require.NotEmpty(t, obj.Metadata.Name)
		require.NotNil(t, obj.Spec)
		assert.Equal(t, obj.Kind, obj.Spec.Kind(), "object %s", obj.Metadata.Name)

		if names[obj.Kind] == nil {
			names[obj.Kind] = map[string]bool{}
		}
		assert.False(t, names[obj.Kind][obj.Metadata.Name], "duplicate %s/%s", obj.Kind, obj.Metadata.Name)
		names[obj.Kind][obj.Metadata.Name] = true
	}
}
