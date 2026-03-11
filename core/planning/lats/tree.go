package lats

import (
	"encoding/xml"
	"math"

	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/google/uuid"
)

type SearchNode struct {
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
		evaluation: &Evaluation{Score: 1},
		parent:     nil,
		children:   make([]*SearchNode, 0, 2),
		reasoning:  []string{reasoning},
		sess:       sess,
	}
}

func newNode(reasoning string) *SearchNode {
	return &SearchNode{id: uuid.New().String(), reasoning: []string{reasoning}}
}

func (n *SearchNode) Expend(node *SearchNode, evaluation *Evaluation) {
	if evaluation == nil { // new candidate
		evaluation = &Evaluation{Score: 1}
	}

	reasoning := make([]string, 0, len(n.reasoning)+len(node.reasoning))
	for _, r := range n.reasoning {
		reasoning = append(reasoning, r)
	}
	reasoning = append(reasoning, node.reasoning...)
	node.reasoning = reasoning

	node.parent = n
	n.children = append(n.children, node)
	node.sess = n.sess.Fork()
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

func (n *SearchNode) BackPropagate(reward int) {
	var crt = n
	for crt != nil {
		crt.visits += 1
		crt.evaluation.Score = reward + (crt.visits-1)*crt.evaluation.Score
		crt = crt.parent
	}
}

func (n *SearchNode) upperConfidenceBound() int {
	if n.evaluation == nil || n.parent == nil {
		return 1
	}
	return int(float64(n.evaluation.Score) * math.Sqrt(math.Log(float64(n.parent.visits))/float64(n.visits)))
}

func (n *SearchNode) GetBestNode() *SearchNode {
	var (
		maxOne   *SearchNode
		maxScore int
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
	Score     int    `json:"score" jsonschema:"required,description=Rate from 1-100 where 1 is incorrect and 100 is correct"`
	IsDone    bool   `json:"is_done" jsonschema:"required,description=Whether the final answer is found yet"`
	Reasoning string `json:"reasoning" jsonschema:"required,description=Your reasoning and analysis in detail DON'T more than 50 words'"`
}

type Candidates struct {
	Candidates []string `json:"candidates" jsonschema:"required,description=List of candidates for the next reasoning step"`
}

type ConversationHistory struct {
	XMLName  xml.Name `xml:"conversation_history"`
	Messages []string `xml:"messages"`
}
