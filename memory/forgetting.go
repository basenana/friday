package memory

import (
	"math"
	"time"
)

type EvaluationResult struct {
	Forget         bool
	MemoryStrength float64
	TimeDecay      float64
	FrequencyBoost float64
}

type ForgettingSystem struct {
	HalfLifeDays      float64
	FrequencyWeight   float64
	DeletionThreshold float64
	MaxUsageCount     float64
}

func (fs *ForgettingSystem) daysSince(t time.Time) float64 {
	duration := time.Since(t)
	return duration.Hours() / 24.0
}

func (fs *ForgettingSystem) calculateTimeDecay(lastUsed time.Time) float64 {
	if fs.HalfLifeDays <= 0 {
		return 1.0
	}

	daysSince := fs.daysSince(lastUsed)
	// R = e^(-t/S)
	return math.Exp(-daysSince / fs.HalfLifeDays)
}

func (fs *ForgettingSystem) calculateFrequencyBoost(usageCount int) float64 {
	if usageCount <= 0 {
		return 0
	}

	if fs.MaxUsageCount <= 0 {
		return 0
	}

	boost := math.Log(1.0+float64(usageCount)) / math.Log(1.0+fs.MaxUsageCount)

	if boost < 0 {
		return 0
	}
	if boost > 1 {
		return 1
	}
	return boost
}

func (fs *ForgettingSystem) calculateMemoryStrength(record *Memory) (strength, timeDecay, freqBoost float64) {
	timeDecay = fs.calculateTimeDecay(record.LastUsedAt)

	freqBoost = fs.calculateFrequencyBoost(record.UsageCount)

	strength = timeDecay * (1.0 + fs.FrequencyWeight*freqBoost)

	if strength < 0 {
		strength = 0
	}
	if strength > 1 {
		strength = 1
	}

	return strength, timeDecay, freqBoost
}

func (fs *ForgettingSystem) Evaluate(record *Memory) EvaluationResult {
	strength, timeDecay, freqBoost := fs.calculateMemoryStrength(record)

	result := EvaluationResult{
		MemoryStrength: strength,
		TimeDecay:      timeDecay,
		FrequencyBoost: freqBoost,
	}

	if strength < fs.DeletionThreshold {
		result.Forget = true
	}

	return result
}

func DefaultCheckMemoryNeedToForget() func(memory *Memory) bool {
	fs := ForgettingSystem{
		HalfLifeDays:      30,
		FrequencyWeight:   0.6,
		DeletionThreshold: 0.1,
		MaxUsageCount:     100,
	}

	return func(memory *Memory) bool {
		return fs.Evaluate(memory).Forget
	}
}
