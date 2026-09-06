package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ComponentReleaseSpecDigest is the canonical identity of the Release
// definition that must be validated before it can be published or used as
// lifecycle evidence. Mutable delivery-source locations are intentionally
// excluded: filename plus SHA-256 and OCI digest remain the content identity.
func ComponentReleaseSpecDigest(release ComponentRelease) string {
	type artifactSpec struct {
		Alias    string `json:"alias"`
		Filename string `json:"filename"`
		SHA256   string `json:"sha256"`
	}
	type imageSpec struct {
		LogicalName string `json:"logicalName"`
		Digest      string `json:"digest"`
	}
	type dependencySpec struct {
		Kind                string             `json:"kind,omitempty"`
		UpstreamComponentID string             `json:"upstreamComponentId"`
		UpstreamReleaseID   string             `json:"upstreamReleaseId"`
		Purpose             string             `json:"purpose"`
		ParameterMappings   []ParameterMapping `json:"parameterMappings"`
	}
	type actionSpec struct {
		ResourceContract    *ResourceContract `json:"resourceContract,omitempty"`
		ID                  string            `json:"id"`
		PreCheckActionID    string            `json:"preCheckActionId"`
		PostCheckActionID   string            `json:"postCheckActionId"`
		Become              bool              `json:"become"`
		GatherFacts         bool              `json:"gatherFacts"`
		Name                string            `json:"name"`
		Kind                ActionKind        `json:"kind"`
		Playbook            string            `json:"playbook"`
		PlaybookSHA256      string            `json:"playbookSha256"`
		Tags                []string          `json:"tags"`
		HostGroup           string            `json:"hostGroup"`
		RequiredCredentials []string          `json:"requiredCredentials"`
		TimeoutSeconds      int               `json:"timeoutSeconds"`
		RiskLevel           RiskLevel         `json:"riskLevel"`
		Destructive         bool              `json:"destructive"`
		Idempotent          bool              `json:"idempotent"`
		FromReleaseID       string            `json:"fromReleaseId"`
		ToReleaseID         string            `json:"toReleaseId"`
	}
	type playbookFileSpec struct {
		Path      string `json:"path"`
		SHA256    string `json:"sha256"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	spec := struct {
		LineID                  string                `json:"lineId"`
		ParentReleaseID         string                `json:"parentReleaseId"`
		TemplateSourceReleaseID string                `json:"templateSourceReleaseId"`
		Version                 string                `json:"version"`
		ReleaseNotes            string                `json:"releaseNotes"`
		Compatibility           ReleaseCompatibility  `json:"compatibility"`
		RiskLevel               RiskLevel             `json:"riskLevel"`
		EnvironmentConstraints  map[string]any        `json:"environmentConstraints"`
		Parameters              []ParameterDefinition `json:"parameters"`
		Dependencies            []dependencySpec      `json:"dependencies"`
		Actions                 []actionSpec          `json:"actions"`
		PlaybookFiles           []playbookFileSpec    `json:"playbookFiles"`
		PlaybookTreeSHA256      string                `json:"playbookTreeSha256"`
		PlaybookWorkspaceRoot   string                `json:"playbookWorkspaceRoot"`
		Artifacts               []artifactSpec        `json:"artifacts"`
		Images                  []imageSpec           `json:"images"`
	}{
		LineID: release.LineID, ParentReleaseID: release.ParentReleaseID, TemplateSourceReleaseID: release.TemplateSourceReleaseID,
		Version: release.Version, ReleaseNotes: release.ReleaseNotes,
		Compatibility: release.Compatibility, RiskLevel: release.RiskLevel,
		EnvironmentConstraints: release.EnvironmentConstraints, Parameters: release.Parameters,
		PlaybookTreeSHA256: release.PlaybookTreeSHA256, PlaybookWorkspaceRoot: release.PlaybookWorkspaceRoot,
	}
	artifacts := append([]ComponentArtifact(nil), release.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Alias < artifacts[j].Alias })
	for _, artifact := range artifacts {
		spec.Artifacts = append(spec.Artifacts, artifactSpec{Alias: artifact.Alias, Filename: artifact.Filename, SHA256: artifact.SHA256})
	}
	images := append([]ComponentImage(nil), release.Images...)
	sort.Slice(images, func(i, j int) bool { return images[i].LogicalName < images[j].LogicalName })
	for _, image := range images {
		spec.Images = append(spec.Images, imageSpec{LogicalName: image.LogicalName, Digest: image.Digest})
	}
	for _, dependency := range release.Dependencies {
		spec.Dependencies = append(spec.Dependencies, dependencySpec{
			UpstreamComponentID: dependency.UpstreamComponentID, UpstreamReleaseID: dependency.UpstreamReleaseID, Purpose: dependency.Purpose,
			ParameterMappings: dependency.ParameterMappings, Kind: dependency.Kind,
		})
	}
	for _, action := range release.Actions {
		spec.Actions = append(spec.Actions, actionSpec{ResourceContract: action.ResourceContract,
			ID: action.ID, PreCheckActionID: action.PreCheckActionID, PostCheckActionID: action.PostCheckActionID, Become: action.Become, GatherFacts: action.GatherFacts, Name: action.Name, Kind: action.Kind, Playbook: action.Playbook, PlaybookSHA256: action.PlaybookSHA256, Tags: action.Tags,
			HostGroup:           action.HostGroup,
			RequiredCredentials: action.RequiredCredentials,
			TimeoutSeconds:      action.TimeoutSeconds, RiskLevel: action.RiskLevel, Destructive: action.Destructive,
			Idempotent:    action.Idempotent,
			FromReleaseID: action.FromReleaseID, ToReleaseID: action.ToReleaseID,
		})
	}
	files := append([]ComponentPlaybookFile(nil), release.PlaybookFiles...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, file := range files {
		spec.PlaybookFiles = append(spec.PlaybookFiles, playbookFileSpec{Path: file.Path, SHA256: file.SHA256, SizeBytes: file.SizeBytes})
	}
	encoded, _ := json.Marshal(spec)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func ScenarioRevisionSpecDigest(revision ScenarioRevision) string {
	if revision.DigestVersion >= ScenarioDigestVersion {
		// Identity and publication state are excluded, while every editable execution
		// contract and immutable upgrade source is included. Version zero retains
		// the exact historic algorithm for immutable Run evidence.
		spec := revision
		spec.ID, spec.ScenarioID, spec.Status = "", "", ""
		spec.Revision, spec.PublicationGeneration = 0, 0
		spec.CreatedAt = time.Time{}
		spec.TestPassedAt, spec.ReleasedAt, spec.DeprecatedAt, spec.AbandonedAt = nil, nil, nil, nil
		if len(spec.EnvironmentConstraints) == 0 {
			spec.EnvironmentConstraints = map[string]any{}
		}
		encoded, _ := json.Marshal(spec)
		digest := sha256.Sum256(encoded)
		return fmt.Sprintf("%x", digest[:])
	}
	return LegacyScenarioRevisionSpecDigest(revision)
}

func LegacyScenarioRevisionSpecDigest(revision ScenarioRevision) string {
	constraints := revision.EnvironmentConstraints
	if len(constraints) == 0 {
		constraints = map[string]any{}
	}
	encoded, _ := json.Marshal(struct {
		Graph                  ScenarioGraph
		EnvironmentConstraints map[string]any
	}{revision.Graph, constraints})
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}
