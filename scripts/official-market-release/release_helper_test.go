package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractPackageRejectsUnsafeEntries(t *testing.T) {
	for _, test := range []struct {
		name     string
		header   tar.Header
		contents string
	}{
		{name: "traversal", header: tar.Header{Name: "../escaped", Typeflag: tar.TypeReg}, contents: "bad"},
		{name: "symlink", header: tar.Header{Name: "artifact", Typeflag: tar.TypeSymlink, Linkname: "../escaped"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "package.nrepkg")
			writeArchive(t, archive, test.header, test.contents)
			destination := filepath.Join(root, "extract")
			if err := os.Mkdir(destination, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := extractPackage(archive, destination); err == nil {
				t.Fatal("unsafe archive entry was accepted")
			}
			if _, err := os.Stat(filepath.Join(root, "escaped")); !os.IsNotExist(err) {
				t.Fatalf("unsafe archive wrote outside extraction root: %v", err)
			}
		})
	}
}

func TestExtractPackageWritesRegularFile(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "package.nrepkg")
	writeArchive(t, archive, tar.Header{Name: "artifacts/plugin", Typeflag: tar.TypeReg, Mode: 0o755}, "plugin")
	destination := filepath.Join(root, "extract")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractPackage(archive, destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "artifacts", "plugin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "plugin" {
		t.Fatalf("artifact = %q", data)
	}
}

func writeArchive(t *testing.T, name string, header tar.Header, contents string) {
	t.Helper()
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	header.Size = int64(len(contents))
	if err := tarWriter.WriteHeader(&header); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
