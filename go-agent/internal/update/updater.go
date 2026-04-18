package update

import (
	"errors"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var ErrRestartRequested = errors.New("restart requested")

func NeedsUpdate(current string, desired string) bool {
	return desired != "" && desired != current
}

func HasValidPackage(pkg *model.VersionPackage) bool {
	if pkg == nil {
		return false
	}
	return strings.TrimSpace(pkg.URL) != "" && strings.TrimSpace(pkg.SHA256) != ""
}
