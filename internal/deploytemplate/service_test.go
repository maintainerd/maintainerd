package deploytemplate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/storage"
)

type fakeRepo struct {
	row storage.DeploymentTemplate
}

func (f *fakeRepo) CreateDeploymentTemplate(_ context.Context, arg storage.CreateDeploymentTemplateParams) (storage.DeploymentTemplate, error) {
	f.row = storage.DeploymentTemplate{
		TemplateUuid: uuid.New(),
		Name:         arg.Name,
		Version:      arg.Version,
		Capability:   arg.Capability,
		Image:        arg.Image,
		Parameters:   arg.Parameters,
		Spec:         arg.Spec,
		Metadata:     arg.Metadata,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	return f.row, nil
}

func (f *fakeRepo) GetDeploymentTemplate(_ context.Context, arg storage.GetDeploymentTemplateParams) (storage.DeploymentTemplate, error) {
	if f.row.Name != arg.Name || f.row.Version != arg.Version {
		return storage.DeploymentTemplate{}, pgx.ErrNoRows
	}
	return f.row, nil
}

func (f *fakeRepo) ListDeploymentTemplates(context.Context, storage.ListDeploymentTemplatesParams) ([]storage.DeploymentTemplate, error) {
	panic("not used")
}
func (f *fakeRepo) CountDeploymentTemplates(context.Context) (int64, error) { panic("not used") }
func (f *fakeRepo) SoftDeleteDeploymentTemplate(context.Context, storage.SoftDeleteDeploymentTemplateParams) error {
	panic("not used")
}

func TestRenderTemplateToWorkloadSpec(t *testing.T) {
	params := []Parameter{
		{Name: "name", Type: "string", Required: true},
		{Name: "port", Type: "int", Default: 8080},
		{Name: "password", Type: "secret", Generate: true},
	}
	paramJSON, err := json.Marshal(params)
	require.NoError(t, err)
	specJSON := []byte(`{
		"name": "{{ .name }}",
		"env": {"PASSWORD": "{{ secretRef .password }}"},
		"ports": [{"container_port": 80, "host_port": "{{ .port }}"}]
	}`)
	repo := &fakeRepo{row: storage.DeploymentTemplate{
		TemplateUuid: uuid.New(),
		Name:         "web",
		Version:      "v1",
		Image:        "nginx:1",
		Parameters:   paramJSON,
		Spec:         specJSON,
		Metadata:     []byte(`{}`),
	}}

	rendered, err := NewService(repo).Render(context.Background(), RenderInput{
		Name:   "web",
		Params: map[string]any{"name": "frontend"},
	})
	require.NoError(t, err)
	assert.Equal(t, "nginx:1", rendered.Spec.Image)
	assert.Equal(t, "frontend", rendered.Spec.Name)
	require.Len(t, rendered.Spec.Ports, 1)
	assert.Equal(t, 8080, rendered.Spec.Ports[0].HostPort)
	assert.Equal(t, "secret://password", rendered.Spec.Env["PASSWORD"])
	assert.NotEmpty(t, rendered.SpecHash)
}

func TestRenderRequiresParametersAndValidatesSpec(t *testing.T) {
	params := []Parameter{{Name: "name", Type: "string", Required: true}}
	paramJSON, err := json.Marshal(params)
	require.NoError(t, err)
	repo := &fakeRepo{row: storage.DeploymentTemplate{
		TemplateUuid: uuid.New(),
		Name:         "bad",
		Version:      "v1",
		Image:        "",
		Parameters:   paramJSON,
		Spec:         []byte(`{"name":"{{ .name }}"}`),
		Metadata:     []byte(`{}`),
	}}

	_, err = NewService(repo).Render(context.Background(), RenderInput{Name: "bad"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parameter")

	_, err = NewService(repo).Render(context.Background(), RenderInput{Name: "bad", Params: map[string]any{"name": "app"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "image")
}
