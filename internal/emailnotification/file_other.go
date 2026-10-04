//go:build !unix

package emailnotification

func settingOpenFlags() int { return 0 }
