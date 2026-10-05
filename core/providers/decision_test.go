package providers

import (
	"context"
	"testing"
)

type fakeDecisionProvider struct{}

func (fakeDecisionProvider) Evaluate(context.Context, DecisionRequest) (DecisionResponse, error) {
	return DecisionResponse{}, nil
}

func TestDecisionProviderIsIndependentFromChatClient(t *testing.T) {
	var provider DecisionProvider = fakeDecisionProvider{}
	if _, ok := any(provider).(Client); ok {
		t.Fatal("DecisionProvider unexpectedly requires the chat Client contract")
	}
}

func TestDecisionQuestionTypesAreDistinct(t *testing.T) {
	questions := []DecisionQuestion{
		NoulQuestion{},
		ChoiceQuestion{},
		ScoreQuestion{},
	}

	for i, question := range questions {
		switch question.(type) {
		case NoulQuestion:
			if i != 0 {
				t.Fatalf("NoulQuestion at index %d", i)
			}
		case ChoiceQuestion:
			if i != 1 {
				t.Fatalf("ChoiceQuestion at index %d", i)
			}
		case ScoreQuestion:
			if i != 2 {
				t.Fatalf("ScoreQuestion at index %d", i)
			}
		default:
			t.Fatalf("unknown question type %T", question)
		}
	}
}

func TestDecisionAnswerTypesAreDistinct(t *testing.T) {
	answers := []DecisionAnswer{
		NoulAnswer{Noul: 0.8},
		ChoiceAnswer{Choice: "yes", Confidence: 0.9},
		ScoreAnswer{Score: 7, Confidence: 0.75},
	}

	for i, answer := range answers {
		switch answer.(type) {
		case NoulAnswer:
			if i != 0 {
				t.Fatalf("NoulAnswer at index %d", i)
			}
		case ChoiceAnswer:
			if i != 1 {
				t.Fatalf("ChoiceAnswer at index %d", i)
			}
		case ScoreAnswer:
			if i != 2 {
				t.Fatalf("ScoreAnswer at index %d", i)
			}
		default:
			t.Fatalf("unknown answer type %T", answer)
		}
	}
}
