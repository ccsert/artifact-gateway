//go:build !linux && !darwin

package localcapacity

func nativeProbe(string) (Filesystem, error) {
	return Filesystem{}, ErrUnsupportedPlatform
}
