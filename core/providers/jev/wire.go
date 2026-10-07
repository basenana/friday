package openrouterdecision

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/providers"
)

type wireRequest struct {
	Model     string                  `json:"model"`
	State     any                     `json:"state"`
	Questions map[string]wireQuestion `json:"questions"`
	SessionID string                  `json:"session_id,omitempty"`
	User      string                  `json:"user,omitempty"`
}

type wireQuestion struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type wireResponse struct {
	ID       string                     `json:"id"`
	Model    string                     `json:"model"`
	Provider string                     `json:"provider"`
	Answers  map[string]json.RawMessage `json:"answers"`
	Usage    wireUsage                  `json:"usage"`
}

type wireUsage struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

type wireAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul"`
	Choice        *string            `json:"choice"`
	Score         *float64           `json:"score"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Legend        map[string]any     `json:"legend"`
}

func encodeRequest(model string, request providers.DecisionRequest) ([]byte, error) {
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("decision model is empty")
	}
	if len(request.Questions) == 0 {
		return nil, fmt.Errorf("decision questions are empty")
	}
	if err := validateJSONValue("state", request.State); err != nil {
		return nil, err
	}

	questions := make(map[string]wireQuestion, len(request.Questions))
	for key, question := range request.Questions {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("decision question key is empty")
		}
		encoded, err := encodeQuestion(key, question)
		if err != nil {
			return nil, err
		}
		questions[key] = encoded
	}

	body, err := json.Marshal(wireRequest{
		Model:     model,
		State:     request.State,
		Questions: questions,
		SessionID: request.SessionID,
		User:      request.User,
	})
	if err != nil {
		return nil, fmt.Errorf("encode decision request: %w", err)
	}
	return body, nil
}

func encodeQuestion(key string, question providers.DecisionQuestion) (wireQuestion, error) {
	var encoded wireQuestion
	switch question := question.(type) {
	case providers.NoulQuestion:
		encoded = wireQuestion{Type: "noul", Instructions: question.Instructions}
		if question.Criteria != nil {
			if question.Criteria.True == nil || question.Criteria.False == nil {
				return wireQuestion{}, fmt.Errorf("decision question %q: noul criteria must include true and false", key)
			}
			if err := validateJSONValue("noul true criteria", question.Criteria.True); err != nil {
				return wireQuestion{}, fmt.Errorf("decision question %q: %w", key, err)
			}
			if err := validateJSONValue("noul false criteria", question.Criteria.False); err != nil {
				return wireQuestion{}, fmt.Errorf("decision question %q: %w", key, err)
			}
			encoded.Criteria = map[string]any{"true": question.Criteria.True, "false": question.Criteria.False}
		}
	case providers.ChoiceQuestion:
		if len(question.Criteria) == 0 {
			return wireQuestion{}, fmt.Errorf("decision question %q: choice criteria are empty", key)
		}
		for choice, criterion := range question.Criteria {
			if strings.TrimSpace(choice) == "" {
				return wireQuestion{}, fmt.Errorf("decision question %q: choice criterion key is empty", key)
			}
			if err := validateJSONValue("choice criterion", criterion); err != nil {
				return wireQuestion{}, fmt.Errorf("decision question %q: %w", key, err)
			}
		}
		encoded = wireQuestion{Type: "choice", Instructions: question.Instructions, Criteria: question.Criteria}
	case providers.ScoreQuestion:
		if len(question.Criteria) == 0 {
			return wireQuestion{}, fmt.Errorf("decision question %q: score criteria are empty", key)
		}
		for _, criterion := range question.Criteria {
			if err := validateJSONValue("score criterion", criterion); err != nil {
				return wireQuestion{}, fmt.Errorf("decision question %q: %w", key, err)
			}
		}
		encoded = wireQuestion{Type: "score", Instructions: question.Instructions, Criteria: question.Criteria}
	default:
		return wireQuestion{}, fmt.Errorf("decision question %q: unsupported type %T", key, question)
	}
	if err := validateJSONValue("instructions", encoded.Instructions); err != nil {
		return wireQuestion{}, fmt.Errorf("decision question %q: %w", key, err)
	}
	return encoded, nil
}

func validateJSONValue(name string, value any) error {
	if value == nil {
		return fmt.Errorf("%s is nil", name)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%s is not JSON serializable: %w", name, err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", name, err)
	}
	switch decoded.(type) {
	case string, map[string]any, []any:
		return nil
	default:
		return fmt.Errorf("%s must be a string, JSON object, or JSON array", name)
	}
}

func decodeResponse(response wireResponse) (providers.DecisionResponse, error) {
	answers := make(map[string]providers.DecisionAnswer, len(response.Answers))
	for key, raw := range response.Answers {
		var answer wireAnswer
		if err := json.Unmarshal(raw, &answer); err != nil {
			return providers.DecisionResponse{}, fmt.Errorf("answer %q: decode: %w", key, err)
		}
		switch answer.Type {
		case "":
			return providers.DecisionResponse{}, fmt.Errorf("answer %q: missing type", key)
		case "noul":
			if answer.Noul == nil {
				return providers.DecisionResponse{}, fmt.Errorf("answer %q: missing noul", key)
			}
			answers[key] = providers.NoulAnswer{Noul: *answer.Noul}
		case "choice":
			if answer.Choice == nil {
				return providers.DecisionResponse{}, fmt.Errorf("answer %q: missing choice", key)
			}
			choice := providers.ChoiceAnswer{Choice: *answer.Choice, Probabilities: answer.Probabilities}
			if answer.Confidence != nil {
				choice.Confidence = *answer.Confidence
			}
			answers[key] = choice
		case "score":
			if answer.Score == nil {
				return providers.DecisionResponse{}, fmt.Errorf("answer %q: missing score", key)
			}
			score := providers.ScoreAnswer{Score: *answer.Score, Probabilities: answer.Probabilities, Legend: answer.Legend}
			if answer.Confidence != nil {
				score.Confidence = *answer.Confidence
			}
			answers[key] = score
		default:
			return providers.DecisionResponse{}, fmt.Errorf("answer %q: unknown type %q", key, answer.Type)
		}
	}
	return providers.DecisionResponse{
		ID:       response.ID,
		Model:    response.Model,
		Provider: response.Provider,
		Answers:  answers,
		Usage: providers.DecisionUsage{
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
			Cost:         response.Usage.Cost,
		},
	}, nil
}
