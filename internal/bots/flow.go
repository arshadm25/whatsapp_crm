// Package bots runs chatbot flows (BRD Phase 2). A flow is a graph of nodes saved as JSON. An
// inbound message that matches a bot's trigger starts a session; the session then walks the
// nodes, sending messages, until it needs an answer from the customer, hands the conversation
// to a person, or ends. Every message a bot sends is stored with origin "bot" and the bot's id.
package bots

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// Node types.
const (
	NodeMessage   = "message"
	NodeButtons   = "buttons"
	NodeQuestion  = "question"
	NodeCondition = "condition"
	NodeSet       = "set"
	NodeTag       = "tag"
	NodeTemplate  = "template"
	NodeHandoff   = "handoff"
	NodeEnd       = "end"
	NodeFlow      = "flow" // sends a WhatsApp Flow and waits for the customer to submit it
)

// Trigger types.
const (
	TriggerKeyword      = "keyword"
	TriggerFirstMessage = "first_message"
	TriggerButtonReply  = "button_reply"
	TriggerAnyMessage   = "any_message"
)

const (
	maxNodes      = 100
	maxBodyLength = 1024 // WhatsApp's limit for the body of an interactive message
	maxTextLength = 4096
	maxButtons    = 3
	maxButtonText = 20
	maxKeywords   = 50
	maxTriggers   = 20
	// maxSteps stops a flow that loops without ever waiting for the customer.
	maxSteps = 25
)

type Flow struct {
	Start    string          `json:"start"`
	Triggers []Trigger       `json:"triggers"`
	Nodes    map[string]Node `json:"nodes"`
}

type Trigger struct {
	Type     string   `json:"type"`
	Keywords []string `json:"keywords,omitempty"`
	Match    string   `json:"match,omitempty"` // keyword: "exact" (default) or "contains"
	ButtonID string   `json:"button_id,omitempty"`
}

type Node struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Next string `json:"next,omitempty"`

	Buttons []Button `json:"buttons,omitempty"` // buttons

	Var  string `json:"var,omitempty"`  // question, condition, set
	Kind string `json:"kind,omitempty"` // question: text (default), number, email or phone

	Op    string `json:"op,omitempty"` // condition: equals, not_equals, contains, exists, gt, lt
	Value string `json:"value,omitempty"`
	Then  string `json:"then,omitempty"`
	Else  string `json:"else,omitempty"`

	Tag string `json:"tag,omitempty"` // tag

	Template *TemplateRef `json:"template,omitempty"` // template

	Reason   string `json:"reason,omitempty"`    // handoff: shown to agents and sent in bot.handoff
	AssignTo string `json:"assign_to,omitempty"` // handoff: user id of the agent to assign

	FlowID string `json:"flow_id,omitempty"` // flow: id of one of the workspace's Flows
	CTA    string `json:"cta,omitempty"`     // flow: button label, 20 characters at most
	Screen string `json:"screen,omitempty"`  // flow: screen to open instead of the first
}

type Button struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Next  string `json:"next,omitempty"`
}

type TemplateRef struct {
	Name     string   `json:"name"`
	Language string   `json:"language"`
	Params   []string `json:"params,omitempty"` // body variables in order; may use {{var}}
}

// ParseFlow decodes and validates a flow.
func ParseFlow(raw []byte) (Flow, error) {
	var f Flow
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, httpx.BadRequest("flow", "flow is not valid: "+err.Error()+".")
	}
	return f, f.Validate()
}

func bad(format string, a ...any) error {
	return httpx.BadRequest("flow", fmt.Sprintf(format, a...))
}

func waits(n Node) bool { return n.Type == NodeButtons || n.Type == NodeQuestion || n.Type == NodeFlow }

// Validate checks the flow is complete and within WhatsApp's limits.
func (f Flow) Validate() error {
	if len(f.Nodes) == 0 {
		return bad("A flow needs at least one node.")
	}
	if len(f.Nodes) > maxNodes {
		return bad("A flow can have at most %d nodes.", maxNodes)
	}
	if _, ok := f.Nodes[f.Start]; !ok {
		return bad("start must be the id of a node.")
	}
	if len(f.Triggers) > maxTriggers {
		return bad("A bot can have at most %d triggers.", maxTriggers)
	}
	for i, t := range f.Triggers {
		switch t.Type {
		case TriggerKeyword:
			if len(t.Keywords) == 0 || len(t.Keywords) > maxKeywords {
				return bad("Trigger %d needs 1 to %d keywords.", i+1, maxKeywords)
			}
			for _, k := range t.Keywords {
				if strings.TrimSpace(k) == "" {
					return bad("Trigger %d has an empty keyword.", i+1)
				}
			}
			if t.Match != "" && t.Match != "exact" && t.Match != "contains" {
				return bad("Trigger %d: match must be exact or contains.", i+1)
			}
		case TriggerButtonReply:
			if t.ButtonID == "" {
				return bad("Trigger %d needs a button_id.", i+1)
			}
		case TriggerFirstMessage, TriggerAnyMessage:
		default:
			return bad("Trigger %d has an unknown type. Use keyword, first_message, button_reply or any_message.", i+1)
		}
	}

	ids := make([]string, 0, len(f.Nodes))
	for id := range f.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := f.Nodes[id]
		if err := f.validateNode(id, n); err != nil {
			return err
		}
	}
	return f.checkLoops(ids)
}

func (f Flow) ref(id, field, target string, required bool) error {
	if target == "" {
		if required {
			return bad("Node %q needs %s.", id, field)
		}
		return nil
	}
	if _, ok := f.Nodes[target]; !ok {
		return bad("Node %q: %s points to %q, which does not exist.", id, field, target)
	}
	return nil
}

func (f Flow) validateNode(id string, n Node) error {
	if id == "" || utf8.RuneCountInString(id) > 64 {
		return bad("Node ids must have 1 to 64 characters.")
	}
	text := func(limit int) error {
		c := utf8.RuneCountInString(n.Text)
		if strings.TrimSpace(n.Text) == "" || c > limit {
			return bad("Node %q: text must have 1 to %d characters.", id, limit)
		}
		return nil
	}
	switch n.Type {
	case NodeMessage:
		if err := text(maxTextLength); err != nil {
			return err
		}
		return f.ref(id, "next", n.Next, false)
	case NodeButtons:
		if err := text(maxBodyLength); err != nil {
			return err
		}
		if len(n.Buttons) == 0 || len(n.Buttons) > maxButtons {
			return bad("Node %q needs 1 to %d buttons.", id, maxButtons)
		}
		seen := map[string]bool{}
		for _, b := range n.Buttons {
			c := utf8.RuneCountInString(b.Title)
			if strings.TrimSpace(b.Title) == "" || c > maxButtonText {
				return bad("Node %q: a button title must have 1 to %d characters.", id, maxButtonText)
			}
			if b.ID == "" || utf8.RuneCountInString(b.ID) > 256 || seen[b.ID] {
				return bad("Node %q: every button needs its own id.", id)
			}
			seen[b.ID] = true
			if err := f.ref(id, "a button's next", b.Next, false); err != nil {
				return err
			}
		}
		return nil
	case NodeQuestion:
		if err := text(maxTextLength); err != nil {
			return err
		}
		if !validVar(n.Var) {
			return bad("Node %q: var must be letters, digits and underscores.", id)
		}
		switch n.Kind {
		case "", "text", "number", "email", "phone":
		default:
			return bad("Node %q: kind must be text, number, email or phone.", id)
		}
		return f.ref(id, "next", n.Next, false)
	case NodeCondition:
		if !validVar(n.Var) {
			return bad("Node %q: var must be letters, digits and underscores.", id)
		}
		switch n.Op {
		case "equals", "not_equals", "contains", "gt", "lt":
		case "exists":
		default:
			return bad("Node %q: op must be equals, not_equals, contains, exists, gt or lt.", id)
		}
		if err := f.ref(id, "then", n.Then, true); err != nil {
			return err
		}
		return f.ref(id, "else", n.Else, false)
	case NodeSet:
		if !validVar(n.Var) {
			return bad("Node %q: var must be letters, digits and underscores.", id)
		}
		return f.ref(id, "next", n.Next, false)
	case NodeTag:
		if t := strings.TrimSpace(n.Tag); t == "" || utf8.RuneCountInString(t) > 50 {
			return bad("Node %q: tag must have 1 to 50 characters.", id)
		}
		return f.ref(id, "next", n.Next, false)
	case NodeTemplate:
		if n.Template == nil || n.Template.Name == "" || n.Template.Language == "" {
			return bad("Node %q needs a template name and language.", id)
		}
		return f.ref(id, "next", n.Next, false)
	case NodeHandoff:
		if n.Text != "" {
			if err := text(maxTextLength); err != nil {
				return err
			}
		}
		return nil
	case NodeEnd:
		if n.Text != "" {
			return text(maxTextLength)
		}
		return nil
	case NodeFlow:
		if err := text(maxBodyLength); err != nil {
			return err
		}
		if _, err := uuid.Parse(n.FlowID); err != nil {
			return bad("Node %q needs the id of one of your Flows.", id)
		}
		if utf8.RuneCountInString(n.CTA) > maxButtonText {
			return bad("Node %q: cta must have at most %d characters.", id, maxButtonText)
		}
		if n.Var != "" && !validVar(n.Var) {
			return bad("Node %q: var must be letters, digits and underscores.", id)
		}
		return f.ref(id, "next", n.Next, false)
	}
	return bad("Node %q has an unknown type.", id)
}

// checkLoops refuses a cycle made only of nodes that never wait for the customer, which would
// send messages forever.
func (f Flow) checkLoops(ids []string) error {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var visit func(id string) bool
	visit = func(id string) bool {
		switch state[id] {
		case visiting:
			return true
		case done:
			return false
		}
		state[id] = visiting
		n := f.Nodes[id]
		if !waits(n) {
			for _, next := range successors(n) {
				if visit(next) {
					return true
				}
			}
		}
		state[id] = done
		return false
	}
	for _, id := range ids {
		if visit(id) {
			return bad("The flow loops without ever waiting for the customer. Add a question or buttons inside the loop.")
		}
	}
	return nil
}

func successors(n Node) []string {
	var out []string
	for _, s := range []string{n.Next, n.Then, n.Else} {
		if s != "" {
			out = append(out, s)
		}
	}
	for _, b := range n.Buttons {
		if b.Next != "" {
			out = append(out, b.Next)
		}
	}
	return out
}

func validVar(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
