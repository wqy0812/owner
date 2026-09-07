package delivery

import (
	"context"
	"errors"
)

type ArtifactLocation struct {
	URL          string
	FileStation  string
	RelativePath string
	ObservedSize *int64
}

type ArtifactIdentity struct {
	SHA256    string
	SizeBytes int64
}

type ArtifactTransfer struct {
	Source   ArtifactLocation
	Target   ArtifactLocation
	Identity ArtifactIdentity
}

type ImageLocation struct {
	Ref            string
	ObservedDigest *string
}
type ImageDigest struct{ Value string }
type ImageTransfer struct {
	Source ImageLocation
	Target ImageLocation
	Digest ImageDigest
	Log    func(string)
}

type ArtifactDelivery interface {
	Probe(context.Context, ArtifactLocation, ArtifactIdentity) error
	Transfer(context.Context, ArtifactTransfer) error
}

type ImageDelivery interface {
	Probe(context.Context, ImageLocation, ImageDigest) error
	Transfer(context.Context, ImageTransfer) error
}

var (
	ErrArtifactIdentityMismatch = errors.New("artifact source SHA-256 does not match content identity")
	ErrDeliveryTargetMissing    = errors.New("delivery target missing")
)
