package service

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type managedCertificateMaterialCleaner interface {
	CleanupManagedCertificateMaterial(context.Context, []storage.ManagedCertificateRow, []storage.ManagedCertificateRow) error
}

func cleanupManagedCertificateMaterialBestEffort(
	ctx context.Context,
	store managedCertificateMaterialCleaner,
	previous []storage.ManagedCertificateRow,
	next []storage.ManagedCertificateRow,
) {
	_ = store.CleanupManagedCertificateMaterial(ctx, previous, next)
}
