//go:build linux

package retention

import "syscall"

// Statfs measures the volume holding dir: the space an unprivileged
// process may still use, of its whole size.
func Statfs(dir string) (Space, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return Space{}, err
	}
	bs := uint64(st.Bsize)
	return Space{Free: st.Bavail * bs, Total: st.Blocks * bs}, nil
}
