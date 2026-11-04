package client

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// refusalReasons maps an HTTP status to the reason shown to agents. Statuses
// not listed use "request failed".
var refusalReasons = map[int]string{
	400: "invalid input",
	401: "access denied for this client",
	403: "access denied for this client",
	404: "not found or not visible to this client",
	409: "revision or state conflict; reread before retrying",
	413: "request too large",
	429: "rate limited; retry later",
	503: "VEDA or upstream temporarily unavailable; retry later",
}

// codeReasons replace the status reason for error codes whose meaning the
// status alone does not convey.
var codeReasons = map[string]string{
	"knowledge_disabled":    "the knowledge graph module is disabled on this VEDA server",
	"knowledge_unavailable": "the knowledge index is temporarily unavailable; retry later",
	"reindex_busy":          "a knowledge reindex is already running; retry later",
	"timeout":               "VEDA did not answer in time; retry with smaller limits",
}

// safeErrorCode matches the fixed identifiers the REST service uses as error
// codes. Anything else is free-form text and is never echoed.
var safeErrorCode = regexp.MustCompile(`^[a-z][a-z_ ]{0,63}$`)

// Refused is an HTTP refusal. Only the status and a short server error code
// are kept; response text is never retained.
type Refused struct {
	Status int
	Code   string
}

// newRefused builds a Refused from a status and the first bytes of the body.
func newRefused(status int, payload []byte) Refused {
	refused := Refused{Status: status}
	var decoded map[string]any
	if json.Unmarshal(payload, &decoded) == nil {
		if code, ok := decoded["error"].(string); ok && safeErrorCode.MatchString(code) {
			refused.Code = code
		}
	}
	return refused
}

// Error implements error without any server-provided text.
func (r Refused) Error() string {
	return fmt.Sprintf("VEDA refused request (HTTP %d)", r.Status)
}

// Message is the sentence returned to the agent.
func (r Refused) Message() string {
	reason, ok := codeReasons[r.Code]
	if !ok {
		reason, ok = refusalReasons[r.Status]
	}
	if !ok {
		reason = "request failed"
	}
	detail := ""
	if r.Code != "" {
		detail = ", " + r.Code
	}
	return fmt.Sprintf("VEDA request refused: %s (HTTP %d%s).", reason, r.Status, detail)
}
