package localcapacity

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func nativeProbe(path string) (Filesystem, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return Filesystem{}, err
	}
	defer func() { _ = unix.Close(fd) }()
	var stat unix.Statfs_t
	if err := unix.Fstatfs(fd, &stat); err != nil {
		return Filesystem{}, err
	}
	var inode unix.Stat_t
	if err := unix.Fstat(fd, &inode); err != nil {
		return Filesystem{}, err
	}
	fs := Filesystem{Kind: Unsupported, Blocks: stat.Blocks, FreeBlocks: stat.Bfree, AvailableBlocks: stat.Bavail}
	switch uint32(stat.Type) {
	case unix.NFS_SUPER_MAGIC, unix.CIFS_SUPER_MAGIC, unix.SMB_SUPER_MAGIC, unix.SMB2_SUPER_MAGIC, unix.AFS_SUPER_MAGIC, unix.NCP_SUPER_MAGIC, unix.CEPH_SUPER_MAGIC, unix.CODA_SUPER_MAGIC, unix.V9FS_MAGIC:
		fs.Kind = Remote
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.TMPFS_MAGIC, unix.OVERLAYFS_SUPER_MAGIC:
		fs.Kind = Supported
	}
	// Prefer the fragment unit when supplied; reject invalid units rather than
	// converting a signed value into a large unsigned byte count.
	unit := stat.Frsize
	if unit == 0 {
		unit = stat.Bsize
	}
	if unit > 0 {
		fs.BlockSize = uint64(unit)
	}
	if stat.Fsid.Val != [2]int32{} {
		// Same descriptor binds the statistics and namespace-local device.
		// Distinct devices must not collapse merely because a clone shares fsid.
		fs.Key = fmt.Sprintf("%x:%x:%x:%x", uint32(stat.Type), inode.Dev, stat.Fsid.Val[0], stat.Fsid.Val[1])
	}
	return fs, nil
}
