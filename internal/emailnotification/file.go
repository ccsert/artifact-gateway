package emailnotification

import "os"

func openRegularSetting(path string, max int64, private bool) (*os.File, error) {
	// The precheck also rejects special paths on platforms without O_NONBLOCK.
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrInvalidConfig
	}
	file, err := os.OpenFile(path, os.O_RDONLY|settingOpenFlags(), 0)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > max || (private && opened.Mode().Perm()&0077 != 0) {
		_ = file.Close()
		return nil, ErrInvalidConfig
	}
	return file, nil
}
