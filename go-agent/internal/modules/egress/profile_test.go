//go:build !integration

package egress

func intPtr(v int) *int {
	return &v
}
