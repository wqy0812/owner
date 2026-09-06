package store

import "codex/platform-demo/internal/domain"

func resourceContractJSON(contract *domain.ResourceContract) any {
	if contract == nil {
		return nil
	}
	return jsonText(contract)
}
