package controlsettings

import (
	"fmt"
	"regexp"
)

func ExampleNewRequestID() {
	requestID, err := NewRequestID()
	if err != nil {
		fmt.Println("error")
		return
	}
	fmt.Println(regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(requestID))
	// Output: true
}
