package deploytemplate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	kitruntime "github.com/maintainerd/kit/runtime"

	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/platform/jsonutil"
	"github.com/maintainerd/core/internal/storage"
)

type Parameter struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Default  any    `json:"default,omitempty"`
	Generate bool   `json:"generate,omitempty"`
}

type Template struct {
	UUID       uuid.UUID      `json:"template_uuid"`
	Name       string         `json:"name"`
	Version    string         `json:"version"`
	Capability string         `json:"capability"`
	Image      string         `json:"image"`
	Parameters []Parameter    `json:"parameters"`
	Spec       map[string]any `json:"spec"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type CreateInput struct {
	Name       string
	Version    string
	Capability string
	Image      string
	Parameters []Parameter
	Spec       map[string]any
	Metadata   map[string]any
}

type RenderInput struct {
	Name    string
	Version string
	Params  map[string]any
}

type Rendered struct {
	Template Template                `json:"template"`
	Params   map[string]any          `json:"params"`
	Spec     kitruntime.WorkloadSpec `json:"spec"`
	SpecHash string                  `json:"spec_hash"`
}

type Service struct{ q Repository }

func NewService(q Repository) *Service { return &Service{q: q} }

func (s *Service) Create(ctx context.Context, in CreateInput) (*Template, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Image) == "" {
		return nil, apperror.NewValidation("name and image are required")
	}
	if in.Version == "" {
		in.Version = "v1"
	}
	params, err := json.Marshal(in.Parameters)
	if err != nil {
		return nil, apperror.NewValidation("invalid parameters")
	}
	spec, err := marshalMap(in.Spec)
	if err != nil {
		return nil, apperror.NewValidation("invalid spec")
	}
	meta, err := marshalMap(in.Metadata)
	if err != nil {
		return nil, apperror.NewValidation("invalid metadata")
	}
	row, err := s.q.CreateDeploymentTemplate(ctx, storage.CreateDeploymentTemplateParams{
		Name:       in.Name,
		Version:    in.Version,
		Capability: in.Capability,
		Image:      in.Image,
		Parameters: params,
		Spec:       spec,
		Metadata:   meta,
	})
	if err != nil {
		return nil, err
	}
	t := toTemplate(row)
	return &t, nil
}

func (s *Service) Get(ctx context.Context, name, version string) (*Template, error) {
	row, err := s.q.GetDeploymentTemplate(ctx, storage.GetDeploymentTemplateParams{Name: name, Version: firstNonEmpty(version, "v1")})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("deployment template")
	}
	if err != nil {
		return nil, err
	}
	t := toTemplate(row)
	return &t, nil
}

func (s *Service) Render(ctx context.Context, in RenderInput) (*Rendered, error) {
	tpl, err := s.Get(ctx, in.Name, in.Version)
	if err != nil {
		return nil, err
	}
	params, err := resolveParams(tpl.Parameters, in.Params)
	if err != nil {
		return nil, apperror.NewValidation(err.Error())
	}
	rendered, err := renderAny(tpl.Spec, params)
	if err != nil {
		return nil, apperror.NewValidation(err.Error())
	}
	renderedMap, ok := rendered.(map[string]any)
	if !ok {
		return nil, apperror.NewValidation("template spec must render to an object")
	}
	b, err := json.Marshal(renderedMap)
	if err != nil {
		return nil, err
	}
	var spec kitruntime.WorkloadSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		return nil, apperror.NewValidation("rendered spec is not a WorkloadSpec")
	}
	if spec.Image == "" {
		spec.Image = tpl.Image
	}
	if spec.Name == "" {
		if name, _ := params["name"].(string); name != "" {
			spec.Name = name
		} else {
			spec.Name = tpl.Name
		}
	}
	if err := spec.Validate(); err != nil {
		return nil, apperror.NewValidation(err.Error())
	}
	hash, err := spec.SpecHash()
	if err != nil {
		return nil, err
	}
	return &Rendered{Template: *tpl, Params: params, Spec: spec, SpecHash: hash}, nil
}

func resolveParams(defs []Parameter, supplied map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range supplied {
		out[k] = v
	}
	for _, p := range defs {
		if p.Name == "" {
			return nil, fmt.Errorf("parameter name is required")
		}
		v, ok := out[p.Name]
		if !ok || v == nil || v == "" {
			if p.Default != nil {
				v = p.Default
			} else if p.Generate && p.Type == "secret" {
				v = p.Name
			} else if p.Required {
				return nil, fmt.Errorf("parameter %q is required", p.Name)
			} else {
				continue
			}
		}
		coerced, err := coerceParam(p, v)
		if err != nil {
			return nil, err
		}
		out[p.Name] = coerced
	}
	return out, nil
}

func coerceParam(p Parameter, v any) (any, error) {
	switch p.Type {
	case "", "string", "secret":
		if s, ok := v.(string); ok {
			return s, nil
		}
		return fmt.Sprint(v), nil
	case "int":
		switch n := v.(type) {
		case int:
			return n, nil
		case int64:
			return int(n), nil
		case float64:
			return int(n), nil
		case json.Number:
			i, err := n.Int64()
			return int(i), err
		case string:
			i, err := strconv.Atoi(n)
			if err != nil {
				return nil, fmt.Errorf("parameter %q must be an int", p.Name)
			}
			return i, nil
		default:
			return nil, fmt.Errorf("parameter %q must be an int", p.Name)
		}
	case "bool":
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return nil, fmt.Errorf("parameter %q must be a bool", p.Name)
	default:
		return nil, fmt.Errorf("parameter %q has unsupported type %q", p.Name, p.Type)
	}
}

func renderAny(v any, params map[string]any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			rendered, err := renderAny(val, params)
			if err != nil {
				return nil, err
			}
			out[k] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(x))
		for _, val := range x {
			rendered, err := renderAny(val, params)
			if err != nil {
				return nil, err
			}
			out = append(out, rendered)
		}
		return out, nil
	case string:
		return renderString(x, params)
	default:
		return x, nil
	}
}

func renderString(s string, params map[string]any) (any, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	if name, ok := exactParamTemplate(s); ok {
		v, exists := params[name]
		if !exists {
			return nil, fmt.Errorf("parameter %q is not defined", name)
		}
		return v, nil
	}
	tpl, err := template.New("spec").
		Option("missingkey=error").
		Funcs(template.FuncMap{"secretRef": func(v any) string { return "secret://" + fmt.Sprint(v) }}).
		Parse(s)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, params); err != nil {
		return nil, err
	}
	return buf.String(), nil
}

func exactParamTemplate(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{{") || !strings.HasSuffix(trimmed, "}}") {
		return "", false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "{{"), "}}"))
	if !strings.HasPrefix(inner, ".") || strings.ContainsAny(inner, " |()") {
		return "", false
	}
	name := strings.TrimPrefix(inner, ".")
	if name == "" {
		return "", false
	}
	return name, true
}

func toTemplate(row storage.DeploymentTemplate) Template {
	var params []Parameter
	_ = json.Unmarshal(row.Parameters, &params)
	return Template{
		UUID:       row.TemplateUuid,
		Name:       row.Name,
		Version:    row.Version,
		Capability: row.Capability,
		Image:      row.Image,
		Parameters: params,
		Spec:       jsonutil.JSONToMap(row.Spec),
		Metadata:   jsonutil.JSONToMap(row.Metadata),
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}

func marshalMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
