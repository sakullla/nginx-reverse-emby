package update

func NeedsUpdate(current string, desired string) bool {
	return desired != "" && desired != current
}
