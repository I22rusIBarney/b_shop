//go:build !unix

package geoip

import "os"

func readFile(path string) ([]byte, func() error, error) {
	b, err := os.ReadFile(path)
	return b, nil, err
}
