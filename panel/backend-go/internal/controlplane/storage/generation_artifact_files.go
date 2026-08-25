package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	generationArtifactDirectory           = "generation-artifacts"
	generationArtifactCompactionMarkerKey = "migration.generation_artifact_external_compaction.v1"
)

func generationArtifactRelativePath(digest string) (string, error) {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !validSHA256(digest) {
		return "", errors.New("generation artifact file digest is invalid")
	}
	return filepath.ToSlash(filepath.Join(generationArtifactDirectory, "sha256", digest)), nil
}

func generationArtifactFilePath(dataRoot, relative, digest string) (string, error) {
	if strings.TrimSpace(dataRoot) == "" {
		return "", errors.New("generation artifact data root is required")
	}
	expected, err := generationArtifactRelativePath(digest)
	if err != nil {
		return "", err
	}
	if filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative))) != expected {
		return "", errors.New("generation artifact external path is not content addressed")
	}
	root, err := filepath.Abs(dataRoot)
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(expected)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("generation artifact external path escapes data root")
	}
	resolvedRoot, rootErr := filepath.EvalSymlinks(root)
	resolvedDirectory, directoryErr := filepath.EvalSymlinks(filepath.Dir(path))
	if rootErr == nil && directoryErr == nil {
		rel, err := filepath.Rel(resolvedRoot, resolvedDirectory)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", errors.New("generation artifact external path resolves outside data root")
		}
		path = filepath.Join(resolvedDirectory, filepath.Base(path))
	} else if rootErr != nil && !errors.Is(rootErr, os.ErrNotExist) {
		return "", rootErr
	} else if directoryErr != nil && !errors.Is(directoryErr, os.ErrNotExist) {
		return "", directoryErr
	}
	return path, nil
}

func writeGenerationArtifactFile(dataRoot, digest string, payload []byte) (string, error) {
	actual := sha256.Sum256(payload)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), digest) {
		return "", errors.New("generation artifact file payload digest is inconsistent")
	}
	relative, err := generationArtifactRelativePath(digest)
	if err != nil {
		return "", err
	}
	path, err := generationArtifactFilePath(dataRoot, relative, digest)
	if err != nil {
		return "", err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	path, err = generationArtifactFilePath(dataRoot, relative, digest)
	if err != nil {
		return "", err
	}
	directory = filepath.Dir(path)
	if err := validateGenerationArtifactFile(path, digest, int64(len(payload))); err == nil {
		return relative, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	temporary, err := os.CreateTemp(directory, ".artifact-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", err
	}
	if _, err := io.Copy(temporary, bytes.NewReader(payload)); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if validationErr := validateGenerationArtifactFile(path, digest, int64(len(payload))); validationErr == nil {
			return relative, nil
		}
		return "", err
	}
	if err := syncDirectory(directory); err != nil {
		return "", err
	}
	return relative, nil
}

func validateGenerationArtifactFile(path, digest string, size int64) error {
	lstat, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return errors.New("generation artifact file must be a regular non-symlink")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return errors.New("generation artifact file size is inconsistent")
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, size+1))
	if err != nil {
		return err
	}
	if written != size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), digest) {
		return errors.New("generation artifact file digest is inconsistent")
	}
	return nil
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *GormStore) materializeGenerationArtifact(row GenerationArtifactRow) (GenerationArtifactRow, error) {
	if err := validateGenerationArtifact(row); err != nil {
		return GenerationArtifactRow{}, err
	}
	if strings.TrimSpace(row.ExternalPath) == "" {
		return row, nil
	}
	path, err := generationArtifactFilePath(s.dataRoot, row.ExternalPath, row.SHA256)
	if err != nil {
		return GenerationArtifactRow{}, err
	}
	lstat, err := os.Lstat(path)
	if err != nil {
		return GenerationArtifactRow{}, err
	}
	if lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return GenerationArtifactRow{}, errors.New("generation artifact external file must be a regular non-symlink")
	}
	file, err := os.Open(path)
	if err != nil {
		return GenerationArtifactRow{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != row.SizeBytes {
		return GenerationArtifactRow{}, errors.New("generation artifact external file size is inconsistent")
	}
	hash := sha256.New()
	var payload bytes.Buffer
	written, err := io.Copy(io.MultiWriter(hash, &payload), io.LimitReader(file, row.SizeBytes+1))
	if err != nil || written != row.SizeBytes || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), row.SHA256) {
		return GenerationArtifactRow{}, errors.New("generation artifact external file digest is inconsistent")
	}
	row.Payload = payload.Bytes()
	return row, nil
}

func (s *GormStore) ExternalizeRuntimeArtifacts(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("generation artifact store is unavailable")
	}
	var externalized int64
	for {
		var row GenerationArtifactRow
		err := s.db.WithContext(ctx).
			Where("kind = ? AND external_path = ?", revisionRuntimeArtifactKind, "").
			Order("id").First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return externalized, nil
		}
		if err != nil {
			return externalized, err
		}
		if err := validateGenerationArtifact(row); err != nil {
			return externalized, err
		}
		relative, err := writeGenerationArtifactFile(s.dataRoot, row.SHA256, row.Payload)
		if err != nil {
			return externalized, fmt.Errorf("externalize generation artifact %q: %w", row.ID, err)
		}
		err = s.writeTransaction(ctx, func(tx *gorm.DB) error {
			result := tx.Model(&GenerationArtifactRow{}).
				Where("id = ? AND kind = ? AND sha256 = ? AND size_bytes = ? AND external_path = ?", row.ID, row.Kind, row.SHA256, row.SizeBytes, "").
				Updates(map[string]any{"payload": []byte{}, "external_path": relative})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				externalized++
			}
			return nil
		})
		if err != nil {
			return externalized, err
		}
	}
}

func (s *GormStore) PruneGenerationArtifactFiles(ctx context.Context, cutoff time.Time) (int, error) {
	if s == nil || s.db == nil || cutoff.IsZero() {
		return 0, errors.New("generation artifact file cleanup requires a store and cutoff")
	}
	var references []string
	if err := s.db.WithContext(ctx).Model(&GenerationArtifactRow{}).
		Where("external_path <> ?", "").Distinct("external_path").Pluck("external_path", &references).Error; err != nil {
		return 0, err
	}
	protected := make(map[string]struct{}, len(references))
	for _, relative := range references {
		protected[filepath.Base(filepath.FromSlash(relative))] = struct{}{}
	}
	root := filepath.Join(s.dataRoot, generationArtifactDirectory, "sha256")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !validSHA256(entry.Name()) {
			continue
		}
		if _, keep := protected[entry.Name()]; keep {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return deleted, err
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		path, err := generationArtifactFilePath(s.dataRoot, filepath.ToSlash(filepath.Join(generationArtifactDirectory, "sha256", entry.Name())), entry.Name())
		if err != nil {
			return deleted, err
		}
		if err := os.Remove(path); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (s *GormStore) compactExternalizedSQLite(ctx context.Context) error {
	if s == nil || s.driver != "sqlite" || s.databaseLifecycle == nil || s.databaseLifecycle.group == nil || s.databaseLifecycle.group.databasePath == "" {
		return nil
	}
	s.databaseLifecycle.group.write.Lock()
	defer s.databaseLifecycle.group.write.Unlock()
	s.sqliteWrite.Lock()
	defer s.sqliteWrite.Unlock()
	if result := s.db.WithContext(ctx).Exec("VACUUM"); result.Error != nil {
		return fmt.Errorf("compact externalized SQLite artifacts: %w", result.Error)
	}
	return nil
}

func (s *GormStore) runtimeArtifactCompactionPending(ctx context.Context) (bool, error) {
	if s == nil || s.driver != "sqlite" {
		return false, nil
	}
	var marker MetaRow
	err := s.db.WithContext(ctx).Where("key = ?", generationArtifactCompactionMarkerKey).First(&marker).Error
	if err == nil && marker.Value == "complete" {
		return false, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	var external int64
	if err := s.db.WithContext(ctx).Model(&GenerationArtifactRow{}).
		Where("kind = ? AND external_path <> ?", revisionRuntimeArtifactKind, "").Count(&external).Error; err != nil {
		return false, err
	}
	return external > 0, nil
}

func (s *GormStore) markRuntimeArtifactCompactionComplete(ctx context.Context) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Save(&MetaRow{Key: generationArtifactCompactionMarkerKey, Value: "complete"}).Error
	})
}
