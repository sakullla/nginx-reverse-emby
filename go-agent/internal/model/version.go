package model

type VersionPackage struct {
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Platform string `json:"platform,omitempty"`
	Filename string `json:"filename,omitempty"`
	Size     int64  `json:"size,omitempty"`
}
