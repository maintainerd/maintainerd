package deploytemplate

import (
	"context"

	"github.com/maintainerd/core/internal/storage"
)

type Repository interface {
	CreateDeploymentTemplate(ctx context.Context, arg storage.CreateDeploymentTemplateParams) (storage.DeploymentTemplate, error)
	GetDeploymentTemplate(ctx context.Context, arg storage.GetDeploymentTemplateParams) (storage.DeploymentTemplate, error)
	ListDeploymentTemplates(ctx context.Context, arg storage.ListDeploymentTemplatesParams) ([]storage.DeploymentTemplate, error)
	CountDeploymentTemplates(ctx context.Context) (int64, error)
	SoftDeleteDeploymentTemplate(ctx context.Context, arg storage.SoftDeleteDeploymentTemplateParams) error
}
