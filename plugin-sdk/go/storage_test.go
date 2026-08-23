package pluginsdk

import "testing"

func TestStorageConfigPathContractIsGeneral(t *testing.T) {
	if PermissionStorageRead != "storage.read" || PermissionStorageWrite != "storage.write" || StorageResourceConfigPath != "config-path" {
		t.Fatal("storage binding contract changed")
	}
}
