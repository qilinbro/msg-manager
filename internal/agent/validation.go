package agent

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func validID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

func validText(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) != "" && !strings.ContainsRune(value, 0)
}

// ValidateInput bounds only already-authorized context. Scope selection and
// explicit disclosure confirmation must happen in the calling backend.
func ValidateInput(input Input) error {
	if !validID(input.ConversationID) || len(input.Messages) < 1 || len(input.Messages) > 20 || len(input.Memories) > 8 {
		return failure(InvalidInput)
	}
	if input.Tone != "" && input.Tone != "natural" && input.Tone != "casual" && input.Tone != "professional" {
		return failure(InvalidInput)
	}
	ids := map[string]bool{}
	for _, message := range input.Messages {
		if !validID(message.ID) || ids[message.ID] || !validText(message.Body, 16000) || (message.Direction != "incoming" && message.Direction != "outgoing") {
			return failure(InvalidInput)
		}
		at, err := time.Parse(time.RFC3339Nano, message.OccurredAt)
		if err != nil || at.Before(time.Unix(0, 0)) || at.After(time.Now().Add(24*time.Hour)) {
			return failure(InvalidInput)
		}
		ids[message.ID] = true
	}
	memories := map[string]bool{}
	for _, memory := range input.Memories {
		if !validID(memory.ID) || memories[memory.ID] || memory.VerificationState != "confirmed" || !validText(memory.Content, 2000) || !validRefs(memory.EvidenceIDs, nil, 20) {
			return failure(InvalidInput)
		}
		memories[memory.ID] = true
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > MaxInputBytes {
		return failure(InvalidInput)
	}
	return nil
}

func validRefs(refs []string, allowed map[string]bool, limit int) bool {
	if len(refs) < 1 || len(refs) > limit {
		return false
	}
	seen := map[string]bool{}
	for _, id := range refs {
		if !validID(id) || seen[id] || allowed != nil && !allowed[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

var riskFlags = map[string]bool{"payment": true, "commitment": true, "sensitive": true, "emotional_conflict": true, "uncertain": true}
var intents = map[string]bool{"greeting": true, "information_request": true, "scheduling": true, "commitment": true, "payment": true, "emotional_conflict": true, "other": true}

// ValidateOutput enforces schema and evidence identity, not semantic truth.
// Memory candidates still require independent confirmation in the backend.
func ValidateOutput(input Input, output Output) error {
	if ValidateInput(input) != nil {
		return failure(InvalidInput)
	}
	if !intents[output.Intent] || !validText(output.Summary, 3000) || !validText(output.Draft, 6000) || output.RiskFlags == nil || len(output.RiskFlags) > 5 || output.MemoryCandidates == nil || len(output.MemoryCandidates) > 5 {
		return failure(InvalidOutput)
	}
	allowed := map[string]bool{}
	for _, message := range input.Messages {
		allowed[message.ID] = true
	}
	if !validRefs(output.EvidenceIDs, allowed, 20) {
		return failure(InvalidOutput)
	}
	seenFlags := map[string]bool{}
	for _, flag := range output.RiskFlags {
		if !riskFlags[flag] || seenFlags[flag] {
			return failure(InvalidOutput)
		}
		seenFlags[flag] = true
	}
	needed := ""
	switch output.Intent {
	case "payment":
		needed = "payment"
	case "commitment", "scheduling":
		needed = "commitment"
	case "emotional_conflict":
		needed = "emotional_conflict"
	case "other":
		needed = "uncertain"
	}
	if needed != "" && !seenFlags[needed] {
		return failure(InvalidOutput)
	}
	seenCandidates := map[string]bool{}
	for _, candidate := range output.MemoryCandidates {
		if !validText(candidate.Content, 1000) || seenCandidates[candidate.Content] || !validRefs(candidate.EvidenceIDs, allowed, 20) {
			return failure(InvalidOutput)
		}
		seenCandidates[candidate.Content] = true
	}
	for _, value := range []string{output.Summary, output.Draft} {
		for _, ch := range value {
			if unicode.IsControl(ch) && ch != '\n' && ch != '\r' && ch != '\t' {
				return failure(InvalidOutput)
			}
		}
	}
	return nil
}
