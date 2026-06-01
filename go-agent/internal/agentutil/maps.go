package agentutil

func CloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func EnsureStringMap(src map[string]string) map[string]string {
	if src == nil {
		return make(map[string]string)
	}
	return src
}
