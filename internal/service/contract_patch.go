package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"fmt"
)

type ReleaseContractPatch struct {
	Section            string                        `json:"section"`
	ExpectedGeneration *int64                        `json:"expectedDefinitionGeneration"`
	Parameters         *[]domain.ParameterDefinition `json:"parameters,omitempty"`
	Dependencies       *[]domain.ComponentDependency `json:"dependencies,omitempty"`
	NewParameters      []domain.ParameterDefinition  `json:"newParameters,omitempty"`
	RemoveParameters   []string                      `json:"removeParameters,omitempty"`
}

func (p *CatalogService) PatchReleaseContract(ctx context.Context, actor domain.User, id string, input ReleaseContractPatch) (domain.ComponentRelease, error) {
	r, err := p.store.GetComponentRelease(ctx, id)
	if err != nil {
		return r, err
	}
	if input.ExpectedGeneration == nil || *input.ExpectedGeneration != r.PublicationGeneration {
		return r, fmt.Errorf("%w: 合同已更新，请重新载入后保存", domain.ErrConflict)
	}
	switch input.Section {
	case "parameters":
		if input.Parameters == nil || input.Dependencies != nil || len(input.NewParameters) > 0 || len(input.RemoveParameters) > 0 {
			return r, fmt.Errorf("%w: 参数编辑仅接受参数定义", domain.ErrInvalid)
		}
		r.Parameters = *input.Parameters
	case "dependencies":
		if input.Dependencies == nil || input.Parameters != nil {
			return r, fmt.Errorf("%w: 依赖编辑仅接受依赖及明确的引用参数变更", domain.ErrInvalid)
		}
		removable := map[string]bool{}
		for _, d := range r.Dependencies {
			for _, m := range d.ParameterMappings {
				removable[m.TargetParameter] = true
			}
		}
		remove := map[string]bool{}
		for _, name := range input.RemoveParameters {
			if !removable[name] {
				return r, fmt.Errorf("%w: 仅可随映射移除其引用参数 %s", domain.ErrInvalid, name)
			}
			remove[name] = true
		}
		kept := []domain.ParameterDefinition{}
		for _, parameter := range r.Parameters {
			if !remove[parameter.Name] {
				kept = append(kept, parameter)
			}
		}
		for _, parameter := range input.NewParameters {
			if _, exists := domain.ParameterByName(r.Parameters, parameter.Name); exists || parameter.ValueProvider != domain.ParameterProviderUpstreamMapping {
				return r, fmt.Errorf("%w: 新增引用参数必须名称唯一并由上游映射提供", domain.ErrInvalid)
			}
			kept = append(kept, parameter)
		}
		r.Parameters, r.Dependencies = kept, *input.Dependencies
	default:
		return r, fmt.Errorf("%w: unknown contract section", domain.ErrInvalid)
	}
	r.ExpectedPublicationGeneration = input.ExpectedGeneration
	return p.UpdateRelease(ctx, actor, id, r)
}
