package cache

import "time"

func timeAfter() <-chan time.Time { return time.After(10 * time.Millisecond) }
