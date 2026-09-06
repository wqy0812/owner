package ansible

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestJobArchiveRejectsUnsafeMembers(t *testing.T) {
	for _, name := range []string{"../escape", "/tmp/escape", "roles/../../escape", "roles\\escape", "link"} {
		t.Run(name, func(t *testing.T) {
			var data bytes.Buffer
			gz := gzip.NewWriter(&data)
			tarball := tar.NewWriter(gz)
			header := &tar.Header{Name: name, Size: 1, Mode: 0600, Typeflag: tar.TypeReg}
			if name == "link" {
				header.Typeflag, header.Size, header.Linkname = tar.TypeSymlink, 0, "/tmp/escape"
			}
			if err := tarball.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if header.Size > 0 {
				_, _ = tarball.Write([]byte("x"))
			}
			_ = tarball.Close()
			_ = gz.Close()
			if bundle, err := ReadJobArchive(&data); err == nil {
				bundle.Close()
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
