//go:build !unix

package console

import "fmt"

func lockStorage(string) (func() error, error) {
	return nil, fmt.Errorf("mounted storage requires a platform with exclusive filesystem locking")
}
