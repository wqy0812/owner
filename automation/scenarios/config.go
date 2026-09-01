package scenarios

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type fixtureConfig struct {
	BaseURL                    string   `json:"baseUrl"`
	EnvironmentID              string   `json:"environmentId"`
	EnvironmentName            string   `json:"environmentName"`
	ComponentUserID            string   `json:"componentUserId"`
	ScenarioUserID             string   `json:"scenarioUserId"`
	EnvironmentUserID          string   `json:"environmentUserId"`
	PlatformSSHHost            string   `json:"platformSshHost"`
	FSSSSHHost                 string   `json:"fssSshHost"`
	NodeSSHUser                string   `json:"nodeSshUser"`
	FileStationEndpoint        string   `json:"fileStationEndpoint"`
	ImageRegistryEndpoint      string   `json:"imageRegistryEndpoint"`
	ExpectedHostCount          int      `json:"expectedHostCount"`
	RequiredEnvironmentGroups  []string `json:"requiredEnvironmentGroups"`
	ScenarioRevisionID         string   `json:"scenarioRevisionId"`
	ScenarioDefinitionSHA256   string   `json:"scenarioDefinitionSha256"`
	ScenarioNodeCount          int      `json:"scenarioNodeCount"`
	ScenarioEdgeCount          int      `json:"scenarioEdgeCount"`
	ScenarioInstallStepCount   int      `json:"scenarioInstallStepCount"`
	ScenarioRollbackStepCount  int      `json:"scenarioRollbackStepCount"`
	RollbackFirstReleaseID     string   `json:"rollbackFirstReleaseId"`
	RollbackSecondReleaseID    string   `json:"rollbackSecondReleaseId"`
	RollbackDependencyGroups   []string `json:"rollbackDependencyGroups"`
	RollbackAbsentPaths        []string `json:"rollbackAbsentPaths"`
	RollbackInactiveServices   []string `json:"rollbackInactiveServices"`
	RollbackAbsentInterfaces   []string `json:"rollbackAbsentInterfaces"`
	RollbackAbsentDockerLabels []string `json:"rollbackAbsentDockerLabels"`
	ControlledComponentID      string   `json:"controlledComponentId"`
	ControlledReleaseID        string   `json:"controlledReleaseId"`
	ControlledComponentSlug    string   `json:"controlledComponentSlug"`
	ControlledVersion          string   `json:"controlledVersion"`
	ControlledPlanDigest       string   `json:"controlledPlanDigest"`
	ControlledLimit            string   `json:"controlledLimit"`
	ControlledFailureTag       string   `json:"controlledFailureTag"`
	ControlledInstallTimeout   int      `json:"controlledInstallTimeoutSeconds"`
	ControlledCleanupTimeout   int      `json:"controlledCleanupTimeoutSeconds"`
	DeliveryComponentID        string   `json:"deliveryComponentId"`
	DeliveryReleaseID          string   `json:"deliveryReleaseId"`
	DeliveryComponentSlug      string   `json:"deliveryComponentSlug"`
	DeliveryVersion            string   `json:"deliveryVersion"`
	DeliveryArtifactAlias      string   `json:"deliveryArtifactAlias"`
	DeliveryArtifactFilename   string   `json:"deliveryArtifactFilename"`
	DeliverySeedAlias          string   `json:"deliverySeedAlias"`
	DeliverySeedFilename       string   `json:"deliverySeedFilename"`
	DeliveryImageLogicalName   string   `json:"deliveryImageLogicalName"`
	ArtifactSHA256             string   `json:"artifactSha256"`
	ArtifactSourceURL          string   `json:"artifactSourceUrl"`
	ArtifactTargetURL          string   `json:"artifactTargetUrl"`
	ArtifactTargetRoot         string   `json:"artifactTargetRoot"`
	ArtifactTargetPath         string   `json:"artifactTargetPath"`
	ImageSourceRef             string   `json:"imageSourceRef"`
	ImageDigest                string   `json:"imageDigest"`
	ImageTargetRegistry        string   `json:"imageTargetRegistry"`
	ImageTargetRepository      string   `json:"imageTargetRepository"`
}

type config struct {
	Enabled       bool
	PreflightOnly bool
	ResetDelivery bool
	PollInterval  time.Duration
	RunTimeout    time.Duration
	OutputDir     string
	Fixture       fixtureConfig
}

func loadConfig() (config, error) {
	cfg := config{Enabled: os.Getenv("CLUSTERFORGE_REAL_E2E") == "1"}
	if !cfg.Enabled {
		return cfg, nil
	}
	projectRoot, err := findProjectRoot()
	if err != nil {
		return cfg, err
	}
	configPath := strings.TrimSpace(os.Getenv("CLUSTERFORGE_REAL_E2E_CONFIG"))
	if configPath == "" || !filepath.IsAbs(configPath) {
		return cfg, fmt.Errorf("CLUSTERFORGE_REAL_E2E_CONFIG must be an absolute path to an external JSON configuration")
	}
	resolvedConfigPath, err := filepath.EvalSymlinks(configPath)
	if err != nil {
		return cfg, fmt.Errorf("resolve external real-scenario configuration: %w", err)
	}
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return cfg, fmt.Errorf("resolve project root: %w", err)
	}
	if pathWithin(resolvedProjectRoot, resolvedConfigPath) {
		return cfg, fmt.Errorf("CLUSTERFORGE_REAL_E2E_CONFIG must be stored outside the repository")
	}
	contents, err := os.ReadFile(resolvedConfigPath)
	if err != nil {
		return cfg, fmt.Errorf("read external real-scenario configuration: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg.Fixture); err != nil {
		return cfg, fmt.Errorf("decode external real-scenario configuration: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return cfg, fmt.Errorf("decode external real-scenario configuration: trailing JSON content is not allowed")
	}
	if err := validateFixtureConfig(cfg.Fixture); err != nil {
		return cfg, err
	}
	cfg.PreflightOnly = os.Getenv("CLUSTERFORGE_REAL_E2E_PREFLIGHT_ONLY") == "1"
	cfg.ResetDelivery = os.Getenv("CLUSTERFORGE_REAL_E2E_RESET_DELIVERY") == "1"
	cfg.PollInterval = durationOr("CLUSTERFORGE_REAL_E2E_POLL_INTERVAL", 2*time.Second)
	cfg.RunTimeout = durationOr("CLUSTERFORGE_REAL_E2E_RUN_TIMEOUT", 90*time.Minute)
	cfg.OutputDir = envOr("CLUSTERFORGE_REAL_E2E_OUTPUT_DIR", filepath.Join(projectRoot, "output", "real-scenario-e2e", time.Now().UTC().Format("20060102T150405Z")))
	if !filepath.IsAbs(cfg.OutputDir) {
		cfg.OutputDir = filepath.Join(projectRoot, cfg.OutputDir)
	}
	if os.Getenv("CLUSTERFORGE_REAL_E2E_CONFIRM") != cfg.Fixture.EnvironmentName {
		return cfg, fmt.Errorf("CLUSTERFORGE_REAL_E2E_CONFIRM must exactly equal the configured environmentName %q", cfg.Fixture.EnvironmentName)
	}
	if !cfg.PreflightOnly && !cfg.ResetDelivery {
		return cfg, fmt.Errorf("full execution requires CLUSTERFORGE_REAL_E2E_RESET_DELIVERY=1")
	}
	return cfg, nil
}

func validateFixtureConfig(value fixtureConfig) error {
	required := map[string]string{
		"baseUrl": value.BaseURL, "environmentId": value.EnvironmentID, "environmentName": value.EnvironmentName,
		"componentUserId": value.ComponentUserID, "scenarioUserId": value.ScenarioUserID, "environmentUserId": value.EnvironmentUserID,
		"platformSshHost": value.PlatformSSHHost, "fssSshHost": value.FSSSSHHost, "nodeSshUser": value.NodeSSHUser,
		"fileStationEndpoint": value.FileStationEndpoint, "imageRegistryEndpoint": value.ImageRegistryEndpoint,
		"scenarioRevisionId": value.ScenarioRevisionID, "rollbackFirstReleaseId": value.RollbackFirstReleaseID,
		"rollbackSecondReleaseId": value.RollbackSecondReleaseID,
		"controlledComponentId":   value.ControlledComponentID, "controlledReleaseId": value.ControlledReleaseID,
		"controlledComponentSlug": value.ControlledComponentSlug, "controlledVersion": value.ControlledVersion,
		"controlledLimit": value.ControlledLimit, "controlledFailureTag": value.ControlledFailureTag,
		"deliveryComponentId": value.DeliveryComponentID, "deliveryReleaseId": value.DeliveryReleaseID,
		"deliveryComponentSlug": value.DeliveryComponentSlug, "deliveryVersion": value.DeliveryVersion,
		"deliveryArtifactAlias": value.DeliveryArtifactAlias, "deliveryArtifactFilename": value.DeliveryArtifactFilename,
		"deliverySeedAlias": value.DeliverySeedAlias, "deliverySeedFilename": value.DeliverySeedFilename,
		"deliveryImageLogicalName": value.DeliveryImageLogicalName, "artifactSourceUrl": value.ArtifactSourceURL,
		"artifactTargetUrl": value.ArtifactTargetURL, "artifactTargetRoot": value.ArtifactTargetRoot, "artifactTargetPath": value.ArtifactTargetPath,
		"imageSourceRef": value.ImageSourceRef, "imageTargetRegistry": value.ImageTargetRegistry,
		"imageTargetRepository": value.ImageTargetRepository,
	}
	for name, item := range required {
		if strings.TrimSpace(item) == "" {
			return fmt.Errorf("external real-scenario configuration field %s is required", name)
		}
	}
	for name, item := range map[string]string{"platformSshHost": value.PlatformSSHHost, "fssSshHost": value.FSSSSHHost} {
		if !safeExternalToken(item, false) {
			return fmt.Errorf("external real-scenario configuration field %s contains unsafe characters", name)
		}
	}
	if !safeSSHUser(value.NodeSSHUser) {
		return fmt.Errorf("external real-scenario configuration field nodeSshUser contains unsafe characters")
	}
	for name, raw := range map[string]string{"baseUrl": value.BaseURL, "artifactSourceUrl": value.ArtifactSourceURL, "artifactTargetUrl": value.ArtifactTargetURL, "imageTargetRegistry": value.ImageTargetRegistry} {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("external real-scenario configuration field %s must be an absolute HTTP(S) URL without credentials, query, or fragment", name)
		}
	}
	for name, digest := range map[string]string{"scenarioDefinitionSha256": value.ScenarioDefinitionSHA256, "controlledPlanDigest": value.ControlledPlanDigest, "artifactSha256": value.ArtifactSHA256} {
		if !validSHA256(digest) {
			return fmt.Errorf("external real-scenario configuration field %s must be a lowercase SHA-256", name)
		}
	}
	if !strings.HasPrefix(value.ImageDigest, "sha256:") || !validSHA256(strings.TrimPrefix(value.ImageDigest, "sha256:")) {
		return fmt.Errorf("external real-scenario configuration field imageDigest must be an OCI SHA-256 digest")
	}
	cleanRoot := filepath.Clean(value.ArtifactTargetRoot)
	cleanTarget := filepath.Clean(value.ArtifactTargetPath)
	if !validExternalPath(value.ArtifactTargetRoot) || cleanRoot == "/" || !validExternalPath(value.ArtifactTargetPath) || cleanTarget == "/" || !pathWithin(cleanRoot, cleanTarget) || cleanTarget == cleanRoot {
		return fmt.Errorf("artifactTargetPath must be an absolute file strictly below artifactTargetRoot")
	}
	if value.ExpectedHostCount <= 0 || value.ScenarioNodeCount <= 0 || value.ScenarioEdgeCount <= 0 || value.ScenarioInstallStepCount <= 0 || value.ScenarioRollbackStepCount <= 0 || value.ControlledInstallTimeout <= 0 || value.ControlledCleanupTimeout <= 0 {
		return fmt.Errorf("external real-scenario configuration counts and timeouts must be positive")
	}
	if len(value.RequiredEnvironmentGroups) == 0 || len(value.RollbackDependencyGroups) == 0 {
		return fmt.Errorf("external real-scenario configuration group lists must not be empty")
	}
	for name, values := range map[string][]string{
		"requiredEnvironmentGroups":  value.RequiredEnvironmentGroups,
		"rollbackDependencyGroups":   value.RollbackDependencyGroups,
		"rollbackInactiveServices":   value.RollbackInactiveServices,
		"rollbackAbsentInterfaces":   value.RollbackAbsentInterfaces,
		"rollbackAbsentDockerLabels": value.RollbackAbsentDockerLabels,
	} {
		for _, item := range values {
			if !safePostconditionToken(item) {
				return fmt.Errorf("external real-scenario configuration field %s contains an unsafe value", name)
			}
		}
	}
	if len(value.RollbackAbsentPaths)+len(value.RollbackInactiveServices)+len(value.RollbackAbsentInterfaces)+len(value.RollbackAbsentDockerLabels) == 0 {
		return fmt.Errorf("external real-scenario configuration must declare at least one rollback postcondition")
	}
	for _, item := range value.RollbackAbsentPaths {
		if !validExternalPath(item) || filepath.Clean(item) == "/" {
			return fmt.Errorf("external real-scenario configuration field rollbackAbsentPaths contains an unsafe path")
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func safeExternalToken(value string, allowSlash bool) bool {
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-:@[]", char) || allowSlash && char == '/' {
			continue
		}
		return false
	}
	return value != ""
}

func safeSSHUser(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char) {
			continue
		}
		return false
	}
	return true
}

func safePostconditionToken(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._:/=@-", char) {
			continue
		}
		return false
	}
	return true
}

func validExternalPath(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && safeExternalToken(value, true)
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (c config) remoteLockPath() string {
	digest := sha256.Sum256([]byte(c.Fixture.EnvironmentID))
	return "/var/lock/clusterforge-real-scenario-e2e-" + hex.EncodeToString(digest[:8])
}

func findProjectRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, statErr := os.Stat(filepath.Join(directory, "go.mod")); statErr == nil && !info.IsDir() {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("cannot locate project root from current directory")
		}
		directory = parent
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func durationOr(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
