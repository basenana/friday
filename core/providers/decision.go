package providers

import "context"

type DecisionProvider interface {
	Evaluate(context.Context, DecisionRequest) (DecisionResponse, error)
}

type DecisionRequest struct {
	State     any
	Questions map[string]DecisionQuestion
	SessionID string
	User      string
}

type DecisionQuestion interface {
	decisionQuestion()
}

type NoulQuestion struct {
	Instructions any
	Criteria     *NoulCriteria
}

type NoulCriteria struct {
	True  any
	False any
}

type ChoiceQuestion struct {
	Instructions any
	Criteria     map[string]any
}

type ScoreQuestion struct {
	Instructions any
	Criteria     []any
}

func (NoulQuestion) decisionQuestion()   {}
func (ChoiceQuestion) decisionQuestion() {}
func (ScoreQuestion) decisionQuestion()  {}

type DecisionResponse struct {
	ID       string
	Model    string
	Provider string
	Answers  map[string]DecisionAnswer
	Usage    DecisionUsage
}

type DecisionUsage struct {
	InputTokens  int64
	OutputTokens int64
	Cost         float64
}

type DecisionAnswer interface {
	decisionAnswer()
}

type NoulAnswer struct {
	Noul float64
}

type ChoiceAnswer struct {
	Choice        string
	Confidence    float64
	Probabilities map[string]float64
}

type ScoreAnswer struct {
	Score         float64
	Confidence    float64
	Probabilities map[string]float64
	Legend        map[string]any
}

func (NoulAnswer) decisionAnswer()   {}
func (ChoiceAnswer) decisionAnswer() {}
func (ScoreAnswer) decisionAnswer()  {}
