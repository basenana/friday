package actor

import (
	"context"
	"fmt"

	"github.com/basenana/friday/core/actor/cards"
	"github.com/basenana/friday/core/actor/events"
)

// cardEmitter is the narrow interface the card tools need from the
// actor. Defining it here (rather than referencing *Actor directly)
// keeps the tools package free of import cycles.
type cardEmitter interface {
	// Emit publishes a Custom event to the actor's subscribers and sink.
	EmitCustom(name, itemID string, payload any)
	// WaitForForm registers a pending form and blocks until the user
	// submits or cancels (or ctx expires). Returns the outcome.
	WaitForForm(ctx context.Context, formID string) (FormOutcome, error)
	// ResolveForm attempts to deliver a form outcome to a waiting
	// tool invocation. Returns ErrUnknownForm when no waiter exists.
	ResolveForm(formID string, outcome FormOutcome) error
}

// FormOutcome is shared between the actor and the request_form tool.
// It mirrors cards.FormOutcome but lives in the actor package to
// avoid pulling cards into callers that only need the actor API.
type FormOutcome struct {
	Values    map[string]any
	Cancelled bool
}

// EmitCard is the helper invoked by the emit_card tool handler. It is
// also exported so tests / advanced callers can emit cards directly
// without going through the tool layer.
func (a *Actor) EmitCard(kind, title string, component map[string]any) (string, error) {
	cardKind := cards.Kind(kind)
	pathValidator := a.filePathValidator
	if usesRichPathValidator(cardKind) {
		pathValidator = a.richPathValidator
	}
	if cardKind == cards.KindDiff {
		pathValidator = a.diffSourcePathValidator
	}
	normalized, err := cards.Default.NormalizeComponent(cardKind, component, pathValidator)
	if err != nil {
		return "", err
	}
	if cardKind == cards.KindDiff && a.unifiedDiffValidator != nil {
		if unifiedDiff, ok := normalized["unified_diff"].(string); ok {
			if err := a.unifiedDiffValidator([]byte(unifiedDiff)); err != nil {
				return "", fmt.Errorf("cards: invalid unified diff: %w", err)
			}
		}
	}
	cardID := globalIDGenerator.Next("card")
	a.EmitCustom(events.CustomCardEmitted, cardID, events.CardEmittedBody{
		CardID:    cardID,
		Kind:      kind,
		Title:     title,
		Component: normalized,
	})
	return cardID, nil
}

func usesRichPathValidator(kind cards.Kind) bool {
	switch kind {
	case cards.KindHTMLPreview, cards.KindImage, cards.KindTable, cards.KindSpreadsheet, cards.KindDiff:
		return true
	default:
		return false
	}
}

// EmitCustom publishes a Custom event on the actor stream / sink.
//
// The payload is the typed body struct (e.g. CardEmittedBody); it is
// serialized to evt.Payload as-is. The discriminator name lives on
// evt.Name.
func (a *Actor) EmitCustom(name, itemID string, payload any) {
	runID, _ := a.currentRunID.Load().(string)
	evt := events.NewEvent(events.KindCustom, runID).WithName(name).
		WithMessageID(itemID).
		WithPayload(payload)
	a.publish(a.currentTurnContext(), evt)
}
