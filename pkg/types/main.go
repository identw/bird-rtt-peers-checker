package types

import (
	"time"
)

type Result struct {
	IP        string
	Alive     bool
	Timestamp time.Time
	Err       error
	Reason    Reason
	Checker   string
}

type Reason string
