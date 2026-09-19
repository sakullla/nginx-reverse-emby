package model

type VersionPackage struct {
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Platform string `json:"platform,omitempty"`
	Filename string `json:"filename,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

const PackageManifestVersion = 1

type PackageManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Filename      string `json:"filename"`
	Platform      string `json:"platform"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
}

type RuntimePackage struct {
	Version  string `json:"version,omitempty"`
	Platform string `json:"platform,omitempty"`
	Arch     string `json:"arch,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	// Staging is true while this process is still downloading or activating a
	// candidate package. Heartbeats must keep reporting the running image.
	Staging bool `json:"staging,omitempty"`
}
