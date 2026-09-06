package service

import "codex/platform-demo/internal/domain"

func preserveActionLegacy(next *domain.ActionDefinition, previous domain.ActionDefinition) {
	next.GatherFacts = previous.GatherFacts
	if previous.ResourceContract == nil || len(previous.ResourceContract.Checks) == 0 {
		return
	}
	var contract domain.ResourceContract
	if next.ResourceContract != nil {
		contract = *next.ResourceContract
	} else {
		contract = *previous.ResourceContract
	}
	contract.Checks = append([]domain.RuntimeCheck(nil), previous.ResourceContract.Checks...)
	next.ResourceContract = &contract
}
