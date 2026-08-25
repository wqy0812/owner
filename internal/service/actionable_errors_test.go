package service

import (
	"errors"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestActionableExistingErrorOnlyWrapsClientErrors(t *testing.T) {
	internal := errors.New("database connection failed")
	if got := actionableExistingError(internal, "release.contract_invalid", "合同错误", "编辑合同", "/components"); got != internal {
		t.Fatalf("internal error was wrapped as actionable: %T %v", got, got)
	}

	conflict := errors.Join(domain.ErrConflict, errors.New("plan changed"))
	got := actionableExistingError(conflict, "execution.plan_changed", "计划已变化", "重新预览", "/components")
	var actionable *domain.ActionableError
	if !errors.As(got, &actionable) || !errors.Is(got, domain.ErrConflict) {
		t.Fatalf("client conflict was not preserved as actionable: %T %v", got, got)
	}
}
