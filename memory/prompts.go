package memory

import (
	"strings"
	"time"
)

const sessionPrompt = `You are analyzing a conversation session to extract valuable memories.

<session>
Session ID: {session_id}
Created At: {session_created_at}
</session>

<workflow>
## Step 1: Update today's daily memory from session history

- Review conversation history across this sessions
- Extract notable events: decisions made, problems solved, new information learned
- Write concise entries to 'memory/YYYY-MM-DD.md' (session created date)
- Format: timestamp + brief description (one line per entry)
- Skip trivial exchanges; focus on what future sessions should know


**Important Note**:
Once the session has been processed, use the following command to archive it to avoid redundant processing.

'''
friday sessions archive {session_id}
'''

## Step 2: Review recent daily memories

- Read 'memory/YYYY-MM-DD.md' files from the last 3-5 days
- Look for patterns, recurring themes, or accumulated insights
- Note anything that seems important enough for long-term retention

## Step 3: Update long-term memory (MEMORY.md)

- Identify content worth preserving: key decisions, user preferences, lessons learned, important context
- Add new entries to MEMORY.md under appropriate sections
- Keep entries concise but informative
- Cross-reference related topics when useful

## Step 4: Prune outdated information

- Remove entries from MEMORY.md that are no longer relevant
- Merge redundant entries
- Keep MEMORY.md lean - quality over quantity

## Step 5: Sync user preferences

- Check session history for any stated preferences or updated context
- Update USER.md accordingly (name, timezone, ongoing projects, etc.)
</workflow>


<guidelines>
1. Skip small talk and routine exchanges
2. Focus on: decisions made, problems solved, new information learned, user preferences expressed
3. Keep entries concise but informative
4. Only include user_preferences when explicitly stated by the user
</guidelines>


<conversation>
{conversation}
</conversation>

`

func buildPrompt(sessionID string, createdAt time.Time, conversation string) string {
	prompt := sessionPrompt
	prompt = strings.ReplaceAll(prompt, "<session_id>", sessionID)
	prompt = strings.ReplaceAll(prompt, "<session_created_at>", createdAt.Format(time.RFC3339))
	prompt = strings.ReplaceAll(prompt, "<conversation>", conversation)
	return prompt
}
