//go:build !windows

package localcredentials

func defaultPath() (string, error) { return "", ErrUnsupported }

func loadToken(string, string) (string, error) { return "", ErrUnsupported }
