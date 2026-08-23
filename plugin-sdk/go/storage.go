package pluginsdk

const (
	// PermissionStorageRead permits read-only access to an explicitly scoped
	// Host storage resource.
	PermissionStorageRead = "storage.read"
	// PermissionStorageWrite permits read-write access to an explicitly scoped
	// Host storage resource.
	PermissionStorageWrite = "storage.write"

	// StorageResourceConfigPath identifies a permission resource whose ID is a
	// JSON Pointer to an absolute Host directory in the instance configuration.
	// The Host resolves and mounts the directory; plugins only consume the path.
	StorageResourceConfigPath = "config-path"
)
