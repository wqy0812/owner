package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

const environmentExportFormat = "clusterforge-environment/v1"

type EnvironmentExportSource struct {
	EnvironmentID   string `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	RevisionID      string `json:"revisionId"`
	Revision        int    `json:"revision"`
}

type EnvironmentTransferSnapshot struct {
	Facts          map[string]any         `json:"facts"`
	Hosts          []InventoryHost        `json:"hosts"`
	Variables      map[string]string      `json:"variables"`
	Parameters     map[string]any         `json:"parameters"`
	CredentialRefs []domain.CredentialRef `json:"credentialRefs"`
}

type EnvironmentExportDocument struct {
	FormatVersion                string                      `json:"formatVersion"`
	ExportedAt                   time.Time                   `json:"exportedAt"`
	ContainsCredentialReferences bool                        `json:"containsCredentialReferences"`
	Source                       EnvironmentExportSource     `json:"source"`
	Snapshot                     EnvironmentTransferSnapshot `json:"snapshot"`
}

func (p *EnvironmentService) ExportRevision(ctx context.Context, user domain.User, environmentID, revisionID string, includeReferences bool) (EnvironmentExportDocument, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return EnvironmentExportDocument{}, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return EnvironmentExportDocument{}, err
	}
	revision, err := p.store.GetEnvironmentRevision(ctx, revisionID)
	if err != nil {
		return EnvironmentExportDocument{}, err
	}
	if revision.EnvironmentID != environmentID {
		return EnvironmentExportDocument{}, fmt.Errorf("%w: revision does not belong to environment", domain.ErrInvalid)
	}
	var inventory InventoryDocument
	if err := json.Unmarshal(revision.Inventory, &inventory); err != nil {
		return EnvironmentExportDocument{}, fmt.Errorf("%w: invalid inventory", domain.ErrInvalid)
	}
	refs := append([]domain.CredentialRef(nil), revision.CredentialRefs...)
	for index := range refs {
		refs[index].Configured = refs[index].Reference != ""
		if !includeReferences {
			refs[index].Reference = ""
		}
	}
	document := EnvironmentExportDocument{FormatVersion: environmentExportFormat, ExportedAt: time.Now().UTC(), ContainsCredentialReferences: includeReferences, Source: EnvironmentExportSource{EnvironmentID: environment.ID, EnvironmentName: environment.Name, RevisionID: revision.ID, Revision: revision.Revision}, Snapshot: EnvironmentTransferSnapshot{Facts: cloneMap(revision.Facts), Hosts: inventory.Hosts, Variables: cloneStringMap(revision.Variables), Parameters: cloneMap(revision.Parameters), CredentialRefs: refs}}
	action := "environment.revision_exported"
	if includeReferences {
		action = "environment.revision_sensitive_exported"
	}
	p.audit.Record(ctx, user, action, "environment", environmentID, map[string]any{"revisionId": revisionID, "includeCredentialReferences": includeReferences})
	return document, nil
}

type EnvironmentImportTarget struct {
	Kind          string `json:"kind"`
	EnvironmentID string `json:"environmentId,omitempty"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
}

type EnvironmentImportRequest struct {
	Document                    EnvironmentExportDocument `json:"document"`
	Target                      EnvironmentImportTarget   `json:"target"`
	ChangeReason                string                    `json:"changeReason"`
	ConfirmCredentialReferences bool                      `json:"confirmCredentialReferences,omitempty"`
	ExpectedPlanDigest          string                    `json:"expectedPlanDigest,omitempty"`
}

type EnvironmentImportPlan struct {
	PlanDigest              string   `json:"planDigest"`
	TargetKind              string   `json:"targetKind"`
	TargetEnvironmentID     string   `json:"targetEnvironmentId,omitempty"`
	TargetCurrentRevisionID string   `json:"targetCurrentRevisionId,omitempty"`
	NextRevision            int      `json:"nextRevision"`
	HostCount               int      `json:"hostCount"`
	VariableCount           int      `json:"variableCount"`
	ParameterCount          int      `json:"parameterCount"`
	CredentialRefCount      int      `json:"credentialRefCount"`
	Changes                 []string `json:"changes"`
	Warnings                []string `json:"warnings"`
}

func validateTransferSnapshot(snapshot *EnvironmentTransferSnapshot) error {
	if err := rejectSensitiveMap(snapshot.Facts, "environment fact"); err != nil {
		return err
	}
	if err := rejectSensitiveMap(snapshot.Parameters, "environment parameter"); err != nil {
		return err
	}
	if len(snapshot.Hosts) > 256 {
		return fmt.Errorf("%w: inventory supports at most 256 hosts", domain.ErrInvalid)
	}
	seenHosts := map[string]bool{}
	for _, host := range snapshot.Hosts {
		if strings.TrimSpace(host.Name) == "" || strings.TrimSpace(host.Address) == "" || len(host.Groups) == 0 {
			return fmt.Errorf("%w: every inventory host requires name, address and groups", domain.ErrInvalid)
		}
		if seenHosts[host.Name] {
			return fmt.Errorf("%w: duplicate inventory host %q", domain.ErrInvalid, host.Name)
		}
		seenHosts[host.Name] = true
	}
	seenRefs := map[string]bool{}
	for _, ref := range snapshot.CredentialRefs {
		if strings.TrimSpace(ref.Name) == "" || !ref.Valid() {
			return fmt.Errorf("%w: credential reference requires a name and valid kind", domain.ErrInvalid)
		}
		if seenRefs[ref.Name] {
			return fmt.Errorf("%w: duplicate credential reference %q", domain.ErrInvalid, ref.Name)
		}
		seenRefs[ref.Name] = true
		if ref.Reference != "" {
			if err := ValidateCredentialRefs([]domain.CredentialRef{ref}); err != nil {
				return err
			}
		}
	}
	variables, err := normalizeEnvironmentVariables(snapshot.Variables, snapshot.CredentialRefs)
	if err != nil {
		return err
	}
	snapshot.Variables = variables
	return nil
}

func prepareEnvironmentImportDocument(document EnvironmentExportDocument) (EnvironmentExportDocument, error) {
	if document.FormatVersion != environmentExportFormat {
		return document, fmt.Errorf("%w: unsupported environment import format", domain.ErrInvalid)
	}
	if err := validateTransferSnapshot(&document.Snapshot); err != nil {
		return document, err
	}
	containsCredentialReferences := false
	for index := range document.Snapshot.CredentialRefs {
		configured := document.Snapshot.CredentialRefs[index].Reference != ""
		document.Snapshot.CredentialRefs[index].Configured = configured
		containsCredentialReferences = containsCredentialReferences || configured
	}
	// The import document is editable input. Derive this safety flag from the
	// actual references instead of trusting a caller-controlled summary field.
	document.ContainsCredentialReferences = containsCredentialReferences
	return document, nil
}

func (p *EnvironmentService) PreviewImport(ctx context.Context, user domain.User, input EnvironmentImportRequest) (EnvironmentImportPlan, error) {
	if err := domain.ValidateRole(user, domain.RoleEnvironmentOwner); err != nil {
		return EnvironmentImportPlan{}, err
	}
	document, err := prepareEnvironmentImportDocument(input.Document)
	if err != nil {
		return EnvironmentImportPlan{}, err
	}
	if err := p.catalogRules.validateEnvironmentFactsCatalog(ctx, document.Snapshot.Facts, true); err != nil {
		return EnvironmentImportPlan{}, err
	}
	inventory, _ := json.Marshal(InventoryDocument{Hosts: document.Snapshot.Hosts})
	if err := p.catalogRules.validateEnvironmentInventoryCatalog(ctx, inventory); err != nil {
		return EnvironmentImportPlan{}, err
	}
	if err := p.validateEnvironmentValues(ctx, document.Snapshot.Parameters, document.Snapshot.Variables, document.Snapshot.CredentialRefs); err != nil {
		return EnvironmentImportPlan{}, err
	}
	plan := EnvironmentImportPlan{
		TargetKind:         input.Target.Kind,
		HostCount:          len(document.Snapshot.Hosts),
		VariableCount:      len(document.Snapshot.Variables),
		ParameterCount:     len(document.Snapshot.Parameters),
		CredentialRefCount: len(document.Snapshot.CredentialRefs),
		Warnings:           make([]string, 0),
	}
	if document.ContainsCredentialReferences {
		plan.Warnings = append(plan.Warnings, "导入文件包含 CredentialRef 引用；提交前必须明确确认复用。")
	}
	for _, ref := range document.Snapshot.CredentialRefs {
		if ref.Reference == "" {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("凭据 %s 尚未映射。", ref.Name))
		}
	}
	var targetRevision string
	var previousFacts map[string]any
	var previousInventory json.RawMessage
	switch input.Target.Kind {
	case "new":
		if strings.TrimSpace(input.Target.Name) == "" {
			return plan, fmt.Errorf("%w: new environment name is required", domain.ErrInvalid)
		}
		plan.NextRevision = 1
		plan.Changes = []string{"创建新环境", "创建初始环境版本 r1"}
	case "existing":
		environment, err := p.store.GetEnvironment(ctx, input.Target.EnvironmentID, false)
		if err != nil {
			return plan, err
		}
		if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
			return plan, err
		}
		if err := ensureEnvironmentActive(environment); err != nil {
			return plan, err
		}
		if environment.Revision == nil {
			return plan, fmt.Errorf("%w: target environment has no revision", domain.ErrConflict)
		}
		if strings.TrimSpace(input.ChangeReason) == "" {
			return plan, fmt.Errorf("%w: change reason is required", domain.ErrInvalid)
		}
		plan.TargetEnvironmentID, plan.TargetCurrentRevisionID, targetRevision = environment.ID, environment.CurrentRevisionID, environment.CurrentRevisionID
		previousFacts = environment.Revision.Facts
		previousInventory = environment.Revision.Inventory
		next, err := p.store.NextEnvironmentRevision(ctx, environment.ID)
		if err != nil {
			return plan, err
		}
		plan.NextRevision = next
		plan.Changes = environmentSnapshotDiff(*environment.Revision, document.Snapshot)
	default:
		return plan, fmt.Errorf("%w: import target kind must be new or existing", domain.ErrInvalid)
	}
	if err := p.catalogRules.validateEnvironmentFactRetiredReferences(ctx, document.Snapshot.Facts, previousFacts); err != nil {
		return plan, err
	}
	options, err := p.catalogRules.platformOptionLookup(ctx)
	if err != nil {
		return plan, err
	}
	if err := options.ValidateInventoryChanges(inventory, previousInventory); err != nil {
		return plan, err
	}
	canonical := input
	canonical.Document = document
	canonical.ExpectedPlanDigest = ""
	canonical.ConfirmCredentialReferences = false
	plan.PlanDigest = digestValue(struct {
		Request        EnvironmentImportRequest
		TargetRevision string
	}{canonical, targetRevision})
	return plan, nil
}

func environmentSnapshotDiff(current domain.EnvironmentRevision, incoming EnvironmentTransferSnapshot) []string {
	changes := make([]string, 0)
	var inventory InventoryDocument
	_ = json.Unmarshal(current.Inventory, &inventory)
	if digestValue(current.Facts) != digestValue(incoming.Facts) {
		changes = append(changes, "Facts 将更新")
	}
	if digestValue(inventory.Hosts) != digestValue(incoming.Hosts) {
		changes = append(changes, "Inventory 将更新")
	}
	if digestValue(current.Variables) != digestValue(incoming.Variables) {
		changes = append(changes, "Variables 将更新")
	}
	if digestValue(current.Parameters) != digestValue(incoming.Parameters) {
		changes = append(changes, "结构化环境参数将更新")
	}
	if digestValue(current.CredentialRefs) != digestValue(incoming.CredentialRefs) {
		changes = append(changes, "CredentialRef 将更新")
	}
	if len(changes) == 0 {
		changes = append(changes, "配置内容无变化，但仍会创建有审计记录的新版本")
	}
	return changes
}

func (p *EnvironmentService) Import(ctx context.Context, user domain.User, input EnvironmentImportRequest) (domain.Environment, error) {
	plan, err := p.PreviewImport(ctx, user, input)
	if err != nil {
		return domain.Environment{}, err
	}
	document, err := prepareEnvironmentImportDocument(input.Document)
	if err != nil {
		return domain.Environment{}, err
	}
	if input.ExpectedPlanDigest == "" || input.ExpectedPlanDigest != plan.PlanDigest {
		return domain.Environment{}, fmt.Errorf("%w: environment import plan changed; preview again", domain.ErrConflict)
	}
	if document.ContainsCredentialReferences && !input.ConfirmCredentialReferences {
		return domain.Environment{}, fmt.Errorf("%w: confirmCredentialReferences is required", domain.ErrInvalid)
	}
	for _, ref := range document.Snapshot.CredentialRefs {
		if ref.Reference == "" {
			return domain.Environment{}, fmt.Errorf("%w: credential %q must be remapped or removed", domain.ErrInvalid, ref.Name)
		}
	}
	inventory, _ := json.Marshal(InventoryDocument{Hosts: document.Snapshot.Hosts})
	now := time.Now().UTC()
	if input.Target.Kind == "new" {
		environment := domain.Environment{ID: newID("environment"), Name: strings.TrimSpace(input.Target.Name), Description: input.Target.Description, OwnerID: user.ID, CreatedAt: now, UpdatedAt: now}
		revision := domain.EnvironmentRevision{ID: newID("environment-revision"), EnvironmentID: environment.ID, Revision: 1, Facts: cloneMap(document.Snapshot.Facts), Inventory: inventory, Variables: cloneStringMap(document.Snapshot.Variables), Parameters: cloneMap(document.Snapshot.Parameters), CredentialRefs: append([]domain.CredentialRef(nil), document.Snapshot.CredentialRefs...), CreatedBy: user.ID, ChangeReason: valueOr(strings.TrimSpace(input.ChangeReason), "从导入文件创建"), CreatedAt: now}
		environment.CurrentRevisionID, environment.Revision = revision.ID, &revision
		if err := p.store.CreateEnvironment(ctx, environment, revision, store.EnvironmentRevisionWrite{RequireCompleteFacts: true}); err != nil {
			return environment, err
		}
		p.audit.Record(ctx, user, "environment.imported", "environment", environment.ID, map[string]any{"revisionId": revision.ID, "planDigest": plan.PlanDigest, "sourceRevisionId": input.Document.Source.RevisionID})
		return environment, nil
	}
	revision := domain.EnvironmentRevision{ID: newID("environment-revision"), EnvironmentID: plan.TargetEnvironmentID, Revision: plan.NextRevision, Facts: cloneMap(document.Snapshot.Facts), Inventory: inventory, Variables: cloneStringMap(document.Snapshot.Variables), Parameters: cloneMap(document.Snapshot.Parameters), CredentialRefs: append([]domain.CredentialRef(nil), document.Snapshot.CredentialRefs...), CreatedBy: user.ID, ChangeReason: strings.TrimSpace(input.ChangeReason), CreatedAt: now}
	if err := p.store.CreateEnvironmentRevision(ctx, revision, store.EnvironmentRevisionWrite{ExpectedCurrentRevisionID: plan.TargetCurrentRevisionID, ValidateAllValues: true, RequireCompleteFacts: true}); err != nil {
		return domain.Environment{}, err
	}
	p.audit.Record(ctx, user, "environment.revision_imported", "environment", plan.TargetEnvironmentID, map[string]any{"revisionId": revision.ID, "revision": revision.Revision, "planDigest": plan.PlanDigest, "sourceRevisionId": input.Document.Source.RevisionID, "changeReason": revision.ChangeReason})
	return p.store.GetEnvironment(ctx, plan.TargetEnvironmentID, true)
}
