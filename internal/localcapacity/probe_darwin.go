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
	fs := Filesystem{Kind: Unsupported, BlockSize: uint64(stat.Bsize), Blocks: stat.Blocks, FreeBlocks: stat.Bfree, AvailableBlocks: stat.Bavail}
	if stat.Flags&unix.MNT_LOCAL == 0 {
		fs.Kind = Remote
	} else {
		switch unix.ByteSliceToString(stat.Fstypename[:]) {
		case "apfs", "hfs":
			fs.Kind = Supported
		}
	}
	if stat.Fsid.Val != [2]int32{} {
		fs.Key = fmt.Sprintf("%x:%x:%x:%x", stat.Type, inode.Dev, stat.Fsid.Val[0], stat.Fsid.Val[1])
	}
	return fs, nil
}
