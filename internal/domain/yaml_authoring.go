package domain

import "fmt"

// These fields remain in historical records and digests. They are not inputs
// to new jobs; only an explicit source-save migration may clear them.
func (a ActionDefinition) NeedsYAMLMigration() bool {
	return a.GatherFacts || (a.ResourceContract != nil && len(a.ResourceContract.Checks) > 0)
}

func (j ScenarioAcceptanceJob) NeedsYAMLMigration() bool {
	return j.GatherFacts || len(j.RuntimeChecks) > 0
}

func YAMLMigrationRequired(name string) error {
	return &CodedError{Code: "authoring.yaml_migration_required", Message: fmt.Sprintf("%s 尚有旧 facts 或固定探测配置，请在草稿中迁入 YAML 并确认保存后重新验证", name), Cause: ErrConflict}
}

func ValidateReleaseYAMLAuthoring(release ComponentRelease) error {
	for _, action := range release.Actions {
		if action.NeedsYAMLMigration() {
			return YAMLMigrationRequired(action.Name)
		}
	}
	return nil
}
