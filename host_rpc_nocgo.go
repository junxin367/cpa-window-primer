//go:build !cgo

package main

import (
	"encoding/json"
	"fmt"
)

func callHostRPC(method string, payload any) (json.RawMessage, error) {
	return nil, fmt.Errorf("host callback %s requires a cgo plugin build", method)
}
