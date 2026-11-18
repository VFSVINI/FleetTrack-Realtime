package handler

import (
	"strconv"
	"sync/atomic"
)

type counter = atomic.Uint64

func itoa(n int) string { return strconv.Itoa(n) }
