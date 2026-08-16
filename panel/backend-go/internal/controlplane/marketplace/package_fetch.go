package marketplace

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

type PackageFetcher interface {
	FetchPackage(context.Context, plugins.MarketEntry, string) error
}

type HTTPPackageFetcher struct {
	Client *http.Client
}

func (f HTTPPackageFetcher) FetchPackage(ctx context.Context, entry plugins.MarketEntry, destination string) error {
	if entry.BlobFormat != "tar+gzip-v1" || entry.BlobSize <= 0 || entry.BlobSize > plugins.DefaultMaxPackageBytes || !isSHA256(entry.BlobSHA256) {
		return errors.New("official package transport metadata is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.PackagePath, nil)
	if err != nil {
		return err
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download official package: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download official package: unexpected HTTP status %d", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != entry.BlobSize {
		return errors.New("official package Content-Length differs from signed index")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	archive, err := os.CreateTemp(filepath.Dir(destination), ".official-package-*.nrepkg")
	if err != nil {
		return err
	}
	archiveName := archive.Name()
	defer os.Remove(archiveName)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(response.Body, entry.BlobSize+1))
	closeErr := archive.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if written != entry.BlobSize || hex.EncodeToString(hash.Sum(nil)) != entry.BlobSHA256 {
		return errors.New("official package blob differs from signed size or SHA-256")
	}
	if err := extractPackageBlob(archiveName, destination); err != nil {
		_ = os.RemoveAll(destination)
		return err
	}
	return nil
}

func extractPackageBlob(archiveName, destination string) error {
	file, err := os.Open(archiveName)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open official package gzip: %w", err)
	}
	defer gzipReader.Close()
	gzipReader.Multistream(false)
	reader := tar.NewReader(gzipReader)
	if err := os.Mkdir(destination, 0o755); err != nil {
		return err
	}
	previous := ""
	files, total := 0, int64(0)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read official package archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || !fs.ValidPath(header.Name) || header.Name <= previous || (header.Mode != 0o644 && header.Mode != 0o755) || header.Size < 0 || header.Size > plugins.DefaultMaxFileBytes {
			return fmt.Errorf("official package archive entry %q is invalid", header.Name)
		}
		files++
		total += header.Size
		if files > plugins.DefaultMaxPackageFiles || total > plugins.DefaultMaxPackageBytes {
			return errors.New("official package archive exceeds package budget")
		}
		target, err := secureCheckoutPath(destination, header.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fs.FileMode(header.Mode))
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(output, reader, header.Size)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		previous = header.Name
	}
	if files == 0 {
		return errors.New("official package archive is empty")
	}
	var extra [1]byte
	if count, err := gzipReader.Read(extra[:]); count != 0 || !errors.Is(err, io.EOF) {
		return errors.New("official package gzip contains trailing data")
	}
	return nil
}

func isSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
