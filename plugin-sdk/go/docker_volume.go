package pluginsdk

// The docker-compose command proxy is the only Docker access path for
// plugins: the sandboxed plugin process executes the proxy CLI, never a real
// Docker client. Volume archive/restore are host-mediated operations in that
// proxy: the host constructs every helper-container argument, validates the
// volume name and app identity, and stages archive files inside the calling
// plugin's workspace so no path outside a managed workspace is ever read or
// written. Access stays gated by the existing
// service.revocable-resource-handle docker-compose/managed grant.
const (
	// DockerVolumeHelperImage is the pinned helper image the proxy uses for
	// volume tar operations. It is a host constant: plugins cannot substitute
	// another image.
	DockerVolumeHelperImage = "docker.io/library/alpine:3.21"

	// DockerVolumeStagingDir is the per-app staging directory, relative to
	// the app workspace root, where volume archives are written and read.
	DockerVolumeStagingDir = "backup-staging/volumes"
)

// DockerProxyRequest is the docker-compose command proxy wire request. The
// sandboxed CLI fills AppID, Compose, and Env from the current workspace.
type DockerProxyRequest struct {
	Args    []string `json:"args"`
	AppID   string   `json:"app_id,omitempty"`
	Compose string   `json:"compose,omitempty"`
	Env     string   `json:"env,omitempty"`
}

// DockerProxyResponse is the docker-compose command proxy wire response.
// ArchivePath is the slash-separated path of a staged volume archive,
// relative to the plugin workspace root, set by volume archive requests.
type DockerProxyResponse struct {
	Output      []byte `json:"output,omitempty"`
	ExitCode    int    `json:"exit_code,omitempty"`
	Error       string `json:"error,omitempty"`
	ArchivePath string `json:"archive_path,omitempty"`
}

// DockerVolumeArchiveArgs builds the proxy arguments that archive one named
// Docker volume into the calling app's staging directory.
func DockerVolumeArchiveArgs(volume string) []string {
	return []string{"volume", "archive", volume}
}

// DockerVolumeRestoreArgs builds the proxy arguments that restore one named
// Docker volume from the calling app's staging directory.
func DockerVolumeRestoreArgs(volume string) []string {
	return []string{"volume", "restore", volume}
}
