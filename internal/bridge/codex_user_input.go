package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
)

// Native JSON-RPC request ids may be numbers or strings. JSON encoding keeps
// those distinct while preserving the original typed id for the response.
func nativeRequestKey(id any) string {
	b, _ := json.Marshal(id)
	return string(b)
}

func decodeCodexUserInput(params map[string]any) ([]protocol.UserInputQuestion, bool, error) {
	blocking, ok := params["isBlocking"].(bool)
	if !ok {
		return nil, false, errors.New("Codex did not specify native blocking semantics")
	}
	rawQuestions, ok := params["questions"].([]any)
	if !ok || len(rawQuestions) == 0 {
		return nil, false, errors.New("Codex sent no questions")
	}
	questions := make([]protocol.UserInputQuestion, 0, len(rawQuestions))
	questionIDs := make(map[string]bool, len(rawQuestions))
	for _, raw := range rawQuestions {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, false, errors.New("invalid Codex question")
		}
		id, _ := entry["id"].(string)
		prompt, _ := entry["question"].(string)
		if id == "" || strings.TrimSpace(prompt) == "" || questionIDs[id] {
			return nil, false, errors.New("invalid or duplicate Codex question id")
		}
		questionIDs[id] = true
		question := protocol.UserInputQuestion{ID: id, Question: prompt, SelectionMode: "single"}
		question.Header, _ = entry["header"].(string)
		question.Secret, _ = entry["isSecret"].(bool)
		question.AllowCustom, _ = entry["isOther"].(bool)
		if rawOptions, ok := entry["options"].([]any); ok {
			optionIDs := make(map[string]bool, len(rawOptions))
			for _, rawOption := range rawOptions {
				option, ok := rawOption.(map[string]any)
				if !ok {
					return nil, false, fmt.Errorf("invalid option for %s", id)
				}
				label, _ := option["label"].(string)
				if strings.TrimSpace(label) == "" || optionIDs[label] {
					return nil, false, fmt.Errorf("invalid or duplicate option for %s", id)
				}
				optionIDs[label] = true
				description, _ := option["description"].(string)
				question.Options = append(question.Options, protocol.UserInputOption{ID: label, Label: label, Description: description})
			}
		}
		if len(question.Options) == 0 {
			question.AllowCustom = true
		}
		questions = append(questions, question)
	}
	return questions, blocking, nil
}

func codexUserInputResult(questions []protocol.UserInputQuestion, answers map[string]protocol.UserInputAnswer) any {
	nativeAnswers := make(map[string]any, len(questions))
	for _, question := range questions {
		answer := answers[question.ID]
		values := answer.OptionIDs
		if answer.CustomText != nil {
			values = []string{*answer.CustomText}
		}
		nativeAnswers[question.ID] = map[string]any{"answers": values}
	}
	return map[string]any{"answers": nativeAnswers}
}

// Async questions are agent messages, not item/tool/requestUserInput server
// requests. Codex identifies each answer by the source message and its index.
func decodeCodexAsyncUserInput(item map[string]any) ([]protocol.UserInputQuestion, error) {
	if item["type"] != "agentMessage" || item["delivery"] != "async" {
		return nil, errors.New("not a Codex async question")
	}
	messageID, _ := item["id"].(string)
	raw, ok := item["questions"].([]any)
	if messageID == "" || !ok || len(raw) == 0 || len(raw) > 64 {
		return nil, errors.New("invalid Codex async question message")
	}
	questions := make([]protocol.UserInputQuestion, 0, len(raw))
	for index, value := range raw {
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("invalid Codex async question")
		}
		title, _ := entry["title"].(string)
		if strings.TrimSpace(title) == "" {
			return nil, errors.New("Codex async question has no title")
		}
		id, err := json.Marshal([]any{"request_user_input_async", messageID, index})
		if err != nil || len(id) > 128 {
			return nil, errors.New("Codex async question id exceeds supported limits")
		}
		question := protocol.UserInputQuestion{ID: string(id), Question: title, SelectionMode: "single", AllowCustom: true}
		if options, present := entry["options"]; present && options != nil {
			labels, ok := options.([]any)
			if !ok || len(labels) > 16 {
				return nil, errors.New("Codex async question options exceed supported limits")
			}
			seen := make(map[string]bool, len(labels))
			for _, value := range labels {
				label, ok := value.(string)
				if !ok || strings.TrimSpace(label) == "" || seen[label] {
					return nil, errors.New("invalid Codex async question option")
				}
				seen[label] = true
				question.Options = append(question.Options, protocol.UserInputOption{ID: label, Label: label})
			}
		}
		questions = append(questions, question)
	}
	return questions, nil
}

func codexAsyncUserInputText(question protocol.UserInputQuestion, answer protocol.UserInputAnswer) (string, error) {
	value := ""
	if answer.CustomText != nil {
		value = *answer.CustomText
	} else if len(answer.OptionIDs) == 1 {
		value = answer.OptionIDs[0]
	}
	if strings.TrimSpace(value) == "" {
		return "", errors.New("Codex async answer is empty")
	}
	// Match Codex's AnsweredQuestion fragment. The question is bounded and
	// flattened so model-authored newlines cannot alter the envelope framing.
	title := []rune(question.Question)
	if len(title) > 512 {
		title = title[:512]
	}
	body, err := json.Marshal([]map[string]string{{"answer": value, "question": strings.NewReplacer("\n", " ", "\r", " ").Replace(string(title)), "questionItemId": question.ID}})
	if err != nil {
		return "", err
	}
	return "<send_user_message_question_reply>\n" + string(body) + "\n</send_user_message_question_reply>", nil
}

func codexUserInputPrompt(prompt string) string {
	return prompt + "\n\nWhen you need the user's decision or missing information, use request_user_input_async if available for questions that may be answered later. Otherwise, use request_user_input if available for a blocking decision. Offer concise choices and allow a custom answer when appropriate. Keep working when the native async tool allows it; wait for the user's answer when it is required to finish."
}
