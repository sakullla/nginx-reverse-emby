package marketplace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

func TestHTTPPackageFetcherDownloadsOnlySelectedSignedBlob(t *testing.T) {
	blob := packageBlobFixture(t, []blobFixtureFile{{name: "artifact/plugin", mode: 0o755, data: []byte("payload")}, {name: "plugin.yaml", mode: 0o644, data: []byte("manifest\n")}})
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Length", fmt.Sprint(len(blob)))
		_, _ = w.Write(blob)
	}))
	defer server.Close()
	digest := sha256.Sum256(blob)
	entry := plugins.MarketEntry{PackagePath: server.URL + "/selected.nrepkg", BlobSHA256: fmt.Sprintf("%x", digest[:]), BlobSize: int64(len(blob)), BlobFormat: "tar+gzip-v1"}
	destination := filepath.Join(t.TempDir(), "package")
	if err := (HTTPPackageFetcher{}).FetchPackage(context.Background(), entry, destination); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "artifact", "plugin"))
	if err != nil || string(data) != "payload" || requests != 1 {
		t.Fatalf("selected package result = %q, requests=%d, err=%v", data, requests, err)
	}
}

func TestHTTPPackageFetcherRejectsTraversalAndBlobTampering(t *testing.T) {
	t.Run("traversal", func(t *testing.T) {
		blob := packageBlobFixture(t, []blobFixtureFile{{name: "../escape", mode: 0o644, data: []byte("bad")}})
		if err := fetchBlobFixture(t, blob, blob); err == nil {
			t.Fatal("archive traversal was accepted")
		}
	})
	t.Run("digest", func(t *testing.T) {
		signed := packageBlobFixture(t, []blobFixtureFile{{name: "plugin.yaml", mode: 0o644, data: []byte("signed")}})
		delivered := append([]byte(nil), signed...)
		delivered[len(delivered)-1] ^= 1
		if err := fetchBlobFixture(t, signed, delivered); err == nil {
			t.Fatal("tampered transport blob was accepted")
		}
	})
}

type blobFixtureFile struct {
	name string
	mode int64
	data []byte
}

func packageBlobFixture(t *testing.T, files []blobFixtureFile) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, file := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(file.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func fetchBlobFixture(t *testing.T, signed, delivered []byte) error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(delivered) }))
	defer server.Close()
	digest := sha256.Sum256(signed)
	entry := plugins.MarketEntry{PackagePath: server.URL, BlobSHA256: fmt.Sprintf("%x", digest[:]), BlobSize: int64(len(signed)), BlobFormat: "tar+gzip-v1"}
	return (HTTPPackageFetcher{}).FetchPackage(context.Background(), entry, filepath.Join(t.TempDir(), "package"))
}
