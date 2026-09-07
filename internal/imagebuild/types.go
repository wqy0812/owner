// Package imagebuild executes isolated Dockerfile builds without platform state.
package imagebuild

type Request struct {
	ImageRef   string
	Dockerfile []byte
}
type Result struct {
	ResolvedRef string
	Digest      string
}
type LogEvent struct {
	Stream  string
	Message string
}
