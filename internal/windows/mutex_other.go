//go:build !windows

package windows

import "fmt"

type Mutex struct{}

func AcquireMutex(string) (*Mutex, error) {
	return nil, fmt.Errorf("worktracker collector is supported only on Windows")
}
func (*Mutex) Close() error { return nil }
