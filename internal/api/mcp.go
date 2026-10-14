package api

import (
	"context"
	"net/http"
	"strings"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/mcpserver"
)

// EnableMCP must be called before serving requests. Configuration is opt-in and
// no browser session or login endpoint is reachable through the MCP adapter.
func (h *Handler) EnableMCP(ctx context.Context, configPath, transferDir string) (*mcpserver.Server, error) {
	s, err := mcpserver.New(ctx, mcpserver.Options{ConfigPath: configPath, TransferDir: transferDir, LookupUser: h.platform.Identity().GetUser, Audit: func(ctx context.Context, a mcpserver.Audit) {
		h.platform.Audit().Record(ctx, domain.User{ID: a.UserID}, "mcp.tool_called", "mcp_tool", a.ToolName, map[string]any{"source": "mcp", "clientId": a.ClientID, "toolName": a.ToolName, "requestId": a.RequestID, "status": a.Status, "durationMs": a.Duration.Milliseconds()})
	}}, h.mcpOperations())
	if err != nil {
		return nil, err
	}
	h.mcp = s
	return s, nil
}

func (h *Handler) mcpBusinessHandler(handle http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := mcpserver.CurrentUser(r.Context())
		if user.ID == "" {
			writeError(w, domain.ErrUnauthorized)
			return
		}
		handle(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	}
}

func (h *Handler) mcpOperation(handlerName, toolName, pattern string, body mcpserver.Schema, queries, fields, files []string, handle http.HandlerFunc) mcpserver.Operation {
	readOnly := mcpReadOnly(pattern, handlerName)
	return mcpserver.Operation{Name: toolName, Pattern: pattern,
		Description: mcpserver.DescribeOperation(strings.ReplaceAll(strings.TrimPrefix(toolName, "clusterforge_"), "_", " "), pattern, mcpNotes[handlerName], readOnly),
		InputSchema: mcpserver.OperationSchema(pattern, body, queries, fields, files, mcpRequiredBody[handlerName]),
		ReadOnly:    readOnly, Idempotent: readOnly, Handle: h.mcpBusinessHandler(handle)}
}

// Previews use POST but do not submit a Run. validateScenario is deliberately
// absent: it persists validation state. Unknown writes remain conservative.
func mcpReadOnly(pattern, name string) bool {
	if strings.HasPrefix(pattern, "GET ") {
		return true
	}
	switch name {
	case "previewComponentImport", "previewReleaseDraft", "previewReleaseTest", "previewScenarioFork", "previewScenarioExecution", "previewScenarioClone", "planCatalogRestore", "previewEnvironmentRollback", "exportEnvironmentRevision", "previewEnvironmentImport", "previewRunCleanup", "previewScenarioJob", "exportScenarioJob", "previewRunRetry":
		return true
	}
	return false
}

var mcpRequiredBody = map[string][]string{
	"createComponent": {"name", "slug", "layer"}, "updateComponent": {"layer"},
	"createScenario": {"name", "slug"}, "createEnvironment": {"name"},
	"renameReleaseLine": {"name"}, "setReleaseCandidate": {"candidate"},
	"createPlatformOptionCategory": {"label"}, "createPlatformOption": {"label"},
	"markNotificationRead": {"read"},
	"updateInventory":      {"hosts"}, "updateFacts": {"facts"}, "updateVariables": {"variables"},
	"updateEnvironmentParameters": {"values"}, "updateCredentialRefs": {"credentialRefs"},
	"startEnvironmentRollback": {"expectedPlanDigest", "confirmEnvironmentName"}, "retryRun": {"expectedPlanDigest"},
	"archiveRuns": {"runIds"}, "previewRunCleanup": {"runIds"}, "cleanupRuns": {"runIds"},
	"createCatalogRepository": {"path"}, "connectCatalogRepository": {"path"},
	"saveScenarioAcceptance": {"expectedRevisionDigest"},
	"createReleaseDraft":     {"mode", "version", "releaseNotes", "expectedPlanDigest"}, "previewReleaseDraft": {"mode", "version", "releaseNotes"},
	"previewScenarioExecution":   {"environmentId", "executionMode"},
	"startScenarioLifecycleTest": {"environmentId", "executionMode", "expectedPlanDigest", "idempotencyKey"},
	"startScenarioLifecycleRun":  {"environmentId", "executionMode", "expectedPlanDigest", "idempotencyKey"},
	"createPreparation":          {"idempotencyKey"},
	"forkScenario":               {"name", "slug", "sourceRevisionId", "expectedPlanDigest"},
	"reopenScenarioRevision":     {"expectedRevisionDigest"}, "saveScenarioUpgradeConstraints": {"expectedRevisionDigest"},
}

var mcpNotes = map[string]string{
	"me":                               "Returns the identity bound to the MCP credential.",
	"listComponents":                   "Default returns summaries. query.view=contracts requests full visible contracts.",
	"listRuns":                         "Use query.page, pageSize, filter, archive and environmentId to bound results.",
	"runActivity":                      "Poll with query.afterId and query.limit (1..2000) for incremental logs and events.",
	"createReleaseDraft":               "First call preview_release_draft; copy its digest to body.expectedPlanDigest.",
	"testRelease":                      "Preview with preview_release_test and preserve its expectedPlanDigest and confirmation requirements. Returns a Run ID.",
	"startScenarioLifecycleTest":       "Preview with preview_scenario_execution using the same inputs. Returns a Run ID.",
	"startScenarioLifecycleRun":        "Preview with preview_scenario_execution using the same inputs. Returns a Run ID.",
	"startEnvironmentRollback":         "First preview_environment_rollback. Supply its digest and the exact environment name in confirmEnvironmentName. Restores real target state.",
	"retryRun":                         "First preview_run_retry. Supply the unchanged plan digest; retries only the platform-approved continuation.",
	"createPreparation":                "Supply idempotencyKey and poll get_preparation until terminal. Preparation is not execution evidence.",
	"saveReleasePlaybookWorkspaceFile": "Preserve expectedSha256 and expectedTreeSha256 returned by workspace reads.",
	"saveScenarioAcceptanceFile":       "Preserve expectedRevisionDigest, expectedSha256 and expectedTreeSha256.",
	"uploadComponentArtifact":          "Upload file as files.artifact.transferId. body.sha256 is required unless files.checksumFile supplies the checksum. Existing FILE_STATION routing and verification apply.",
	"uploadReleasePlaybook":            "files.playbook references an upload. body.action is a JSON encoded ActionDefinition; body.actionKind selects the action.",
	"startComponentImageBuild":         "files.dockerfile references an upload; body.environmentId and body.tag select the build destination. Poll get_component_image_build.",
	"downloadRunJob":                   "query.verified=true requires verified job eligibility; outputs a transfer reference.",
	"cleanupRuns":                      "Inspect preview_run_cleanup first; deletion cannot be undone by MCP.",
	"restoreCatalogRepository":         "First plan_catalog_restore. Existing backup configuration and restore gates apply.",
}
