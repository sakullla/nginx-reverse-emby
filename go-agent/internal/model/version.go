package model

type VersionPackage struct {
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Platform string `json:"platform,omitempty"`
	Filename string `json:"filename,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

type RuntimePackage struct {
	Version  string `json:"version,omitempty"`
	Platform string `json:"platform,omitempty"`
	Arch     string `json:"arch,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
}
