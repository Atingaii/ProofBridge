package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
)

// Claude's permission callback names the tool separately from its input. Some
// CLI versions wrap that input as tool_input or input, so retain the original
// questions object rather than reconstructing it from browser-facing fields.
func claudePermissionTool(raw json.RawMessage, fallback string) (string, json.RawMessage) {
	var wrapper map[string]json.RawMessage
	if json.Unmarshal(raw, &wrapper) != nil {
		return fallback, raw
	}
	name := fallback
	for _, key := range []string{"tool_name", "toolName", "name"} {
		var value string
		if json.Unmarshal(wrapper[key], &value) == nil && value != "" {
			name = value
			break
		}
	}
	if _, ok := wrapper["questions"]; ok {
		return name, raw
	}
	for _, key := range []string{"tool_input", "toolInput", "input"} {
		if nested := wrapper[key]; len(nested) > 0 && nested[0] == '{' {
			return name, nested
		}
	}
	return name, raw
}

func decodeClaudeUserInput(raw json.RawMessage) ([]protocol.UserInputQuestion, json.RawMessage, error) {
	var original struct {
		Questions []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(raw, &original); err != nil || len(original.Questions) == 0 {
		return nil, nil, errors.New("Claude sent no supported questions")
	}
	var originalObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &originalObject); err != nil || len(originalObject["questions"]) == 0 {
		return nil, nil, errors.New("Claude question input is not an object")
	}
	questions := make([]protocol.UserInputQuestion, 0, len(original.Questions))
	seenQuestions := make(map[string]bool, len(original.Questions))
	for index, item := range original.Questions {
		if strings.TrimSpace(item.Question) == "" || seenQuestions[item.Question] {
			return nil, nil, errors.New("empty or duplicate Claude question text")
		}
		seenQuestions[item.Question] = true
		question := protocol.UserInputQuestion{
			ID: fmt.Sprintf("q%d", index), Header: item.Header, Question: item.Question,
			SelectionMode: "single", AllowCustom: true,
		}
		if item.MultiSelect {
			question.SelectionMode = "multiple"
		}
		seenOptions := make(map[string]bool, len(item.Options))
		for _, option := range item.Options {
			if strings.TrimSpace(option.Label) == "" || seenOptions[option.Label] {
				return nil, nil, fmt.Errorf("empty or duplicate option for Claude question %d", index)
			}
			seenOptions[option.Label] = true
			question.Options = append(question.Options, protocol.UserInputOption{
				ID: option.Label, Label: option.Label, Description: option.Description,
			})
		}
		questions = append(questions, question)
	}
	return questions, originalObject["questions"], nil
}

func claudeUserInputResult(questions []protocol.UserInputQuestion, originalQuestions json.RawMessage, answers map[string]protocol.UserInputAnswer) (json.RawMessage, error) {
	nativeAnswers := make(map[string]any, len(questions))
	for _, question := range questions {
		answer := answers[question.ID]
		if question.SelectionMode == "multiple" {
			values := append([]string(nil), answer.OptionIDs...)
			if answer.CustomText != nil {
				values = append(values, *answer.CustomText)
			}
			nativeAnswers[question.Question] = values
		} else if answer.CustomText != nil {
			nativeAnswers[question.Question] = *answer.CustomText
		} else {
			nativeAnswers[question.Question] = answer.OptionIDs[0]
		}
	}
	return json.Marshal(map[string]any{"questions": json.RawMessage(originalQuestions), "answers": nativeAnswers})
}
