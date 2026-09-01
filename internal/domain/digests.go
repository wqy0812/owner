package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
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
		UpstreamComponentID string             `json:"upstreamComponentId"`
		UpstreamReleaseID   string             `json:"upstreamReleaseId"`
		Purpose             string             `json:"purpose"`
		ParameterMappings   []ParameterMapping `json:"parameterMappings"`
	}
	type actionSpec struct {
		Name                string     `json:"name"`
		Kind                ActionKind `json:"kind"`
		Playbook            string     `json:"playbook"`
		PlaybookSHA256      string     `json:"playbookSha256"`
		Tags                []string   `json:"tags"`
		Limit               string     `json:"limit"`
		HostGroup           string     `json:"hostGroup"`
		AllowedParameters   []string   `json:"allowedParameters"`
		RequiredCredentials []string   `json:"requiredCredentials"`
		TimeoutSeconds      int        `json:"timeoutSeconds"`
		RiskLevel           RiskLevel  `json:"riskLevel"`
		Destructive         bool       `json:"destructive"`
		Idempotent          bool       `json:"idempotent"`
		FromReleaseID       string     `json:"fromReleaseId"`
		ToReleaseID         string     `json:"toReleaseId"`
	}
	spec := struct {
		LineID                  string                `json:"lineId"`
		ParentReleaseID         string                `json:"parentReleaseId"`
		TemplateSourceReleaseID string                `json:"templateSourceReleaseId"`
		Version                string                `json:"version"`
		ReleaseNotes           string                `json:"releaseNotes"`
		Compatibility          ReleaseCompatibility  `json:"compatibility"`
		RiskLevel              RiskLevel             `json:"riskLevel"`
		EnvironmentConstraints map[string]any        `json:"environmentConstraints"`
		Parameters             []ParameterDefinition `json:"parameters"`
		Dependencies           []dependencySpec      `json:"dependencies"`
		Actions                []actionSpec          `json:"actions"`
		Artifacts              []artifactSpec        `json:"artifacts"`
		Images                 []imageSpec           `json:"images"`
	}{
		LineID: release.LineID, ParentReleaseID: release.ParentReleaseID, TemplateSourceReleaseID: release.TemplateSourceReleaseID,
		Version: release.Version, ReleaseNotes: release.ReleaseNotes,
		Compatibility: release.Compatibility, RiskLevel: release.RiskLevel,
		EnvironmentConstraints: release.EnvironmentConstraints, Parameters: release.Parameters,
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
			ParameterMappings: dependency.ParameterMappings,
		})
	}
	for _, action := range release.Actions {
		spec.Actions = append(spec.Actions, actionSpec{
			Name: action.Name, Kind: action.Kind, Playbook: action.Playbook, PlaybookSHA256: action.PlaybookSHA256, Tags: action.Tags,
			Limit: action.Limit, HostGroup: action.HostGroup, AllowedParameters: action.AllowedParameters,
			RequiredCredentials: action.RequiredCredentials,
			TimeoutSeconds:      action.TimeoutSeconds, RiskLevel: action.RiskLevel, Destructive: action.Destructive,
			Idempotent:    action.Idempotent,
			FromReleaseID: action.FromReleaseID, ToReleaseID: action.ToReleaseID,
		})
	}
	encoded, _ := json.Marshal(spec)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func ScenarioRevisionSpecDigest(revision ScenarioRevision) string {
	encoded, _ := json.Marshal(struct{ Graph ScenarioGraph }{Graph: revision.Graph})
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}
