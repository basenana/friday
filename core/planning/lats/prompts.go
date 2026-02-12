package lats

const (
	DEFAULT_REFLECTION_PROMPT = `Given a query and a conversation trajectory, evaluate two things regarding whether the conversation answers the question:
- **correctness**: Whether the thoughts and actions so far are correctly answering the query, even if the answer is not found yet.Rate from 1-100, where 1 is incorrect and 100 is correct.
- **completeness**: Whether the answer to the user's question was found (even if the root cause was not resolved).
Provide your reasoning and analysis in detail, use the same language as the input content and not more than 50 words.
Focus on the latest thought, action, and observation.
Incomplete trajectories can be correct if the thoughts and actions so far are correct, \
even if the answer is not found yet.
If the user's question is seriously out of scope, you can end the conversation as soon as possible.
Do not generate additional thoughts or actions.

Query: {query}
Conversation History:
{conversation_history}
`

	DEFAULT_CANDIDATES_PROMPT = `
Given a query and a conversation trajectory, provide a list of candidates {num_candidates} for the next reasoning step.
Provide as much diversity as possible between candidate steps.
Focus on the latest thought, action, and observation.
Do not generate additional thoughts or actions.

Query: {query}
Conversation History:
{conversation_history}
`
)
