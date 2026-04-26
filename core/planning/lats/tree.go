package lats

import (
	"context"
	"encoding/xml"
	"math"
	"sync"

	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
	"github.com/google/uuid"
)

type SearchNode struct {
	mu         sync.Mutex
	id         string
	visits     int
	evaluation *Evaluation
	parent     *SearchNode
	children   []*SearchNode
	reasoning  []string
	sess      *session.Session
}

func newRoot(reasoning string, sess *session.Session) *SearchNode {
	sess.AppendMessage(&types.Message{Role: types.RoleAgent, Content: reasoning})
	return &SearchNode{
		id:         uuid.New().String(),
		evaluation: &Evaluation{Score: 1.0},
		parent:     nil,
		children:   make([]*SearchNode, 0, 2),
		reasoning:  []string{reasoning},
		sess:       sess,
	}
}

func newNode(reasoning string) *SearchNode {
	return &SearchNode{id: uuid.New().String(), reasoning: []string{reasoning}}
}

func (n *SearchNode) Expand(ctx context.Context, node *SearchNode, evaluation *Evaluation) {
	if evaluation == nil { // new candidate
		evaluation = &Evaluation{Score: 1.0}
	}

	reasoning := make([]string, 0, len(n.reasoning)+len(node.reasoning))
	for _, r := range n.reasoning {
		reasoning = append(reasoning, r)
	}
	reasoning = append(reasoning, node.reasoning...)
	node.reasoning = reasoning

	node.parent = n
	n.mu.Lock()
	n.children = append(n.children, node)
	n.mu.Unlock()
	node.sess = n.sess.Fork()
	tracing.SpanFromContext(ctx).AddEvent("session.fork",
		tracing.String("session.id", node.sess.ID),
		tracing.String("parent_session.id", n.sess.ID),
		tracing.String("session.root_id", node.sess.Root.ID),
		tracing.String("source", "lats"),
	)
	if msg := node.Latest(); msg != "" {
		node.sess.AppendMessage(&types.Message{Role: types.RoleAssistant, Content: msg})
	}
	node.sess.AppendMessage(&types.Message{Role: types.RoleAgent, Content: evaluation.Reasoning})
	node.evaluation = evaluation
	node.BackPropagate(evaluation.Score)
}

func (n *SearchNode) Latest() string {
	if len(n.reasoning) == 0 {
		return ""
	}
	return n.reasoning[len(n.reasoning)-1]
}

func (n *SearchNode) BackPropagate(reward float64) {
	var crt = n
	for crt != nil {
		crt.mu.Lock()
		crt.visits += 1
		// Running average: (reward + (visits-1)*oldScore) / visits
		crt.evaluation.Score = (reward + float64(crt.visits-1)*crt.evaluation.Score) / float64(crt.visits)
		crt.mu.Unlock()
		crt = crt.parent
	}
}

func (n *SearchNode) upperConfidenceBound() float64 {
	if n.evaluation == nil || n.parent == nil {
		return 1.0
	}
	n.mu.Lock()
	score := n.evaluation.Score
	visits := n.visits
	n.mu.Unlock()

	if visits == 0 {
		return math.MaxFloat64 // unvisited nodes get highest priority
	}

	n.parent.mu.Lock()
	parentVisits := n.parent.visits
	n.parent.mu.Unlock()

	if parentVisits == 0 {
		return score
	}

	exploitation := score
	exploration := 2.0 * math.Sqrt(math.Log(float64(parentVisits))/float64(visits))
	return exploitation + exploration
}

func (n *SearchNode) GetBestNode() *SearchNode {
	var (
		maxOne   *SearchNode
		maxScore float64
	)
	for _, child := range n.children {
		if child.evaluation == nil || child.evaluation.IsDone {
			continue
		}

		cScore := child.upperConfidenceBound()
		if cScore > maxScore {
			maxOne = child
			maxScore = cScore
		}
	}

	if maxOne == nil {
		return n
	}

	return maxOne.GetBestNode()
}

type Evaluation struct {
	Score     float64 `json:"score" jsonschema:"required,description=Rate from 1-100 where 1 is incorrect and 100 is correct"`
	IsDone    bool    `json:"is_done" jsonschema:"required,description=Whether the final answer is found yet"`
	Reasoning string  `json:"reasoning" jsonschema:"required,description=Your reasoning and analysis in detail DON'T more than 50 words'"`
}

type Candidates struct {
	Candidates []string `json:"candidates" jsonschema:"required,description=List of candidates for the next reasoning step"`
}

type ConversationHistory struct {
	XMLName  xml.Name `xml:"conversation_history"`
	Messages []string `xml:"messages"`
}
