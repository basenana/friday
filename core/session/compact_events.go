package session

import (
	"strconv"
	"time"
)

// CompactTrigger identifies why a history compaction was attempted.
type CompactTrigger string

const (
	CompactTriggerSoft        CompactTrigger = "soft"
	CompactTriggerHard        CompactTrigger = "hard"
	CompactTriggerThreshold   CompactTrigger = "threshold"
	CompactTriggerManual      CompactTrigger = "manual"
	CompactTriggerOverflow    CompactTrigger = "overflow"
	CompactTriggerPlanHandoff CompactTrigger = "plan_handoff"
	CompactTriggerRefocus     CompactTrigger = "refocus"

	compactEventMaxErrorRunes = 512
	compactErrorTruncSuffix   = "...[truncated]"
)

// CompactFinishData builds a successful compact.finish payload.
func CompactFinishData(method string, trigger CompactTrigger, tokensBefore, tokensAfter int64, dur time.Duration) map[string]string {
	return map[string]string{
		"method":        method,
		"trigger":       string(trigger),
		"status":        "ok",
		"tokens_before": strconv.FormatInt(tokensBefore, 10),
		"tokens_after":  strconv.FormatInt(tokensAfter, 10),
		"saved_tokens":  strconv.FormatInt(tokensBefore-tokensAfter, 10),
		"duration_ms":   strconv.FormatInt(dur.Milliseconds(), 10),
	}
}

// CompactSkipData builds a compact.skip payload.
func CompactSkipData(method string, trigger CompactTrigger, reason string, tokensBefore int64, dur time.Duration) map[string]string {
	return map[string]string{
		"method":        method,
		"trigger":       string(trigger),
		"reason":        reason,
		"tokens_before": strconv.FormatInt(tokensBefore, 10),
		"duration_ms":   strconv.FormatInt(dur.Milliseconds(), 10),
	}
}

// CompactFailData builds a failed compact.finish payload.
func CompactFailData(method string, trigger CompactTrigger, tokensBefore int64, dur time.Duration, err error) map[string]string {
	message := ""
	if err != nil {
		message = truncateCompactError(err.Error())
	}
	return map[string]string{
		"method":        method,
		"trigger":       string(trigger),
		"status":        "failed",
		"tokens_before": strconv.FormatInt(tokensBefore, 10),
		"duration_ms":   strconv.FormatInt(dur.Milliseconds(), 10),
		"error":         message,
	}
}

func truncateCompactError(value string) string {
	runes := []rune(value)
	if len(runes) <= compactEventMaxErrorRunes {
		return value
	}
	suffix := []rune(compactErrorTruncSuffix)
	return string(runes[:compactEventMaxErrorRunes-len(suffix)]) + compactErrorTruncSuffix
}
