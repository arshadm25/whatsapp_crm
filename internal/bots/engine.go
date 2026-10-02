package bots

import (
	"net/mail"
	"regexp"
	"strconv"
	"strings"
)

// Input is what the customer sent.
type Input struct {
	Text     string            // text body, or the title of a tapped button or list row
	ButtonID string            // id of a tapped reply button or list row
	Flow     map[string]string // answers the customer submitted in a WhatsApp Flow
}

// State is a session's position and variables.
type State struct {
	NodeID string // the node waiting for an answer; empty before the first step
	Vars   map[string]string
}

// Contact supplies {{contact.name}} and {{contact.phone}}.
type Contact struct {
	Name  string
	Phone string
}

// Action is one thing the runner must do.
type Action struct {
	Kind     string // send, template, tag, handoff
	Type     string // send: text or interactive
	Content  map[string]any
	Template *TemplateRef // template, with params already filled in
	Tag      string
	FlowNode *Node  // flow: the node to send
	Reason   string // handoff
	AssignTo string // handoff
	Preview  string // short text for the conversation list
}

const (
	ActionSend     = "send"
	ActionTemplate = "template"
	ActionTag      = "tag"
	ActionHandoff  = "handoff"
	ActionFlow     = "flow"
)

// Outcome of one run.
type Outcome struct {
	Actions []Action
	State   State
	Status  string // active, completed, handed_off or failed
	Reason  string
}

const (
	StatusActive    = "active"
	StatusCompleted = "completed"
	StatusHandedOff = "handed_off"
	StatusFailed    = "failed"
	StatusStopped   = "stopped"
	StatusExpired   = "expired"
)

const retriesVar = "_retries"
const maxRetries = 2

var (
	placeholder = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)
	phoneRE     = regexp.MustCompile(`^\+?[0-9][0-9 \-]{6,18}[0-9]$`)
)

func fill(s string, vars map[string]string, c Contact) string {
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		key := placeholder.FindStringSubmatch(m)[1]
		switch key {
		case "contact.name":
			return c.Name
		case "contact.phone":
			return c.Phone
		}
		return vars[key]
	})
}

// AIResult is what the AI step got back for a customer's question.
type AIResult struct {
	Answered bool
	Text     string
	Reason   string // why there is no answer: low_confidence, no_knowledge, limit, error, not_configured
}

// Env is what a run needs from outside the flow: who the customer is, and the AI that answers
// the AI step's questions.
type Env struct {
	Contact Contact
	AI      func(question string, n Node) AIResult
}

const aiTurnsVar = "_ai_turns"
const defaultAITurns = 5

// Start resolves the first step of a session: it walks the flow from its start node.
func Start(f Flow, env Env) Outcome {
	return run(f, State{Vars: map[string]string{}}, nil, env)
}

// Resume continues a session that was waiting for an answer at st.NodeID.
func Resume(f Flow, st State, in Input, env Env) Outcome {
	if st.Vars == nil {
		st.Vars = map[string]string{}
	}
	return run(f, st, &in, env)
}

func run(f Flow, st State, in *Input, env Env) Outcome {
	c := env.Contact
	out := Outcome{State: st, Status: StatusActive}
	cur := f.Start
	if in != nil && f.Nodes[st.NodeID].Type == NodeAI {
		// The AI step keeps answering questions until it has no answer, or has answered enough.
		n := f.Nodes[st.NodeID]
		res := AIResult{Reason: "unsupported"}
		if env.AI != nil && strings.TrimSpace(in.Text) != "" {
			res = env.AI(in.Text, n)
		} else if env.AI == nil {
			res.Reason = "not_configured"
		}
		if !res.Answered {
			st.Vars["_ai_miss"] = res.Reason
			if n.Else == "" {
				out.Actions = append(out.Actions, Action{Kind: ActionHandoff, Reason: "ai_" + res.Reason})
				return finish(out, StatusHandedOff, "ai_"+res.Reason)
			}
			cur = n.Else
		} else {
			out.Actions = append(out.Actions, textAction(res.Text))
			turns := atoi(st.Vars[aiTurnsVar]) + 1
			limit := n.MaxTurns
			if limit <= 0 {
				limit = defaultAITurns
			}
			if turns < limit {
				st.Vars[aiTurnsVar] = strconv.Itoa(turns)
				out.State.NodeID = st.NodeID
				return out
			}
			delete(st.Vars, aiTurnsVar)
			cur = n.Next
			if cur == "" {
				return finish(out, StatusCompleted, "completed")
			}
		}
	} else if in != nil {
		n, ok := f.Nodes[st.NodeID]
		if !ok {
			return fail(out, "node_missing")
		}
		next, answered := answer(n, st.Vars, *in)
		if !answered {
			// The reply did not fit the question: ask again, then give up and hand over.
			tries := atoi(st.Vars[retriesVar]) + 1
			st.Vars[retriesVar] = strconv.Itoa(tries)
			if tries > maxRetries {
				out.Actions = append(out.Actions, Action{Kind: ActionHandoff, Reason: "no_understood_reply"})
				return finish(out, StatusHandedOff, "no_understood_reply")
			}
			cur = st.NodeID
		} else {
			delete(st.Vars, retriesVar)
			cur = next
			if cur == "" {
				return finish(out, StatusCompleted, "completed")
			}
		}
	}

	for steps := 0; steps < maxSteps; steps++ {
		n, ok := f.Nodes[cur]
		if !ok {
			return fail(out, "node_missing")
		}
		switch n.Type {
		case NodeMessage:
			body := fill(n.Text, st.Vars, c)
			out.Actions = append(out.Actions, textAction(body))
			cur = n.Next
		case NodeButtons:
			out.Actions = append(out.Actions, buttonsAction(n, fill(n.Text, st.Vars, c)))
			out.State.NodeID = cur
			return out
		case NodeQuestion:
			out.Actions = append(out.Actions, textAction(fill(n.Text, st.Vars, c)))
			out.State.NodeID = cur
			return out
		case NodeAI:
			if n.Text != "" {
				out.Actions = append(out.Actions, textAction(fill(n.Text, st.Vars, c)))
			}
			st.Vars[aiTurnsVar] = "0"
			out.State.NodeID = cur
			return out
		case NodeFlow:
			node := n
			node.Text = fill(n.Text, st.Vars, c)
			out.Actions = append(out.Actions, Action{Kind: ActionFlow, FlowNode: &node, Preview: node.Text})
			out.State.NodeID = cur
			return out
		case NodeCondition:
			if holds(n, st.Vars) {
				cur = n.Then
			} else {
				cur = n.Else
			}
		case NodeSet:
			st.Vars[n.Var] = fill(n.Value, st.Vars, c)
			cur = n.Next
		case NodeTag:
			out.Actions = append(out.Actions, Action{Kind: ActionTag, Tag: strings.TrimSpace(n.Tag)})
			cur = n.Next
		case NodeTemplate:
			t := *n.Template
			t.Params = make([]string, len(n.Template.Params))
			for i, p := range n.Template.Params {
				t.Params[i] = fill(p, st.Vars, c)
			}
			out.Actions = append(out.Actions, Action{Kind: ActionTemplate, Template: &t, Preview: "Template: " + t.Name})
			cur = n.Next
		case NodeHandoff:
			if n.Text != "" {
				out.Actions = append(out.Actions, textAction(fill(n.Text, st.Vars, c)))
			}
			reason := n.Reason
			if reason == "" {
				reason = "handoff"
			}
			out.Actions = append(out.Actions, Action{Kind: ActionHandoff, Reason: reason, AssignTo: n.AssignTo})
			return finish(out, StatusHandedOff, reason)
		case NodeEnd:
			if n.Text != "" {
				out.Actions = append(out.Actions, textAction(fill(n.Text, st.Vars, c)))
			}
			return finish(out, StatusCompleted, "completed")
		default:
			return fail(out, "unknown_node")
		}
		if cur == "" {
			return finish(out, StatusCompleted, "completed")
		}
	}
	return fail(out, "too_many_steps")
}

func finish(o Outcome, status, reason string) Outcome {
	o.State.NodeID = ""
	o.Status, o.Reason = status, reason
	return o
}

func fail(o Outcome, reason string) Outcome { return finish(o, StatusFailed, reason) }

// answer applies the customer's reply to the node that asked, returning the next node.
func answer(n Node, vars map[string]string, in Input) (next string, ok bool) {
	switch n.Type {
	case NodeButtons:
		for _, b := range n.Buttons {
			if (in.ButtonID != "" && b.ID == in.ButtonID) || (in.ButtonID == "" && strings.EqualFold(strings.TrimSpace(in.Text), b.Title)) {
				return b.Next, true
			}
		}
	case NodeFlow:
		if in.Flow == nil {
			return "", false
		}
		for k, v := range in.Flow {
			if n.Var != "" {
				k = n.Var + "_" + k
			}
			vars[k] = v
		}
		return n.Next, true
	case NodeQuestion:
		v := strings.TrimSpace(in.Text)
		if v == "" || !validAnswer(n.Kind, v) {
			return "", false
		}
		vars[n.Var] = v
		return n.Next, true
	}
	return "", false
}

func validAnswer(kind, v string) bool {
	switch kind {
	case "number":
		_, err := strconv.ParseFloat(v, 64)
		return err == nil
	case "email":
		a, err := mail.ParseAddress(v)
		return err == nil && a.Address == v
	case "phone":
		return phoneRE.MatchString(v)
	}
	return true
}

func holds(n Node, vars map[string]string) bool {
	got, present := vars[n.Var]
	switch n.Op {
	case "exists":
		return present && got != ""
	case "equals":
		return strings.EqualFold(got, n.Value)
	case "not_equals":
		return !strings.EqualFold(got, n.Value)
	case "contains":
		return strings.Contains(strings.ToLower(got), strings.ToLower(n.Value))
	case "gt", "lt":
		a, e1 := strconv.ParseFloat(got, 64)
		b, e2 := strconv.ParseFloat(n.Value, 64)
		if e1 != nil || e2 != nil {
			return false
		}
		if n.Op == "gt" {
			return a > b
		}
		return a < b
	}
	return false
}

func textAction(body string) Action {
	return Action{
		Kind: ActionSend, Type: "text", Preview: body,
		Content: map[string]any{"type": "text", "text": map[string]any{"body": body, "preview_url": false}},
	}
}

func buttonsAction(n Node, body string) Action {
	buttons := make([]map[string]any, len(n.Buttons))
	for i, b := range n.Buttons {
		buttons[i] = map[string]any{"type": "reply", "reply": map[string]any{"id": b.ID, "title": b.Title}}
	}
	return Action{
		Kind: ActionSend, Type: "interactive", Preview: body,
		Content: map[string]any{"type": "interactive", "interactive": map[string]any{
			"type":   "button",
			"body":   map[string]any{"text": body},
			"action": map[string]any{"buttons": buttons},
		}},
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// Matches reports whether an inbound message starts the bot.
func (f Flow) Matches(in Input, firstMessage bool) bool {
	if in.Flow != nil {
		return false // a submitted form belongs to the bot that sent it
	}
	text := strings.ToLower(strings.TrimSpace(in.Text))
	for _, t := range f.Triggers {
		switch t.Type {
		case TriggerAnyMessage:
			return true
		case TriggerFirstMessage:
			if firstMessage {
				return true
			}
		case TriggerButtonReply:
			if in.ButtonID != "" && in.ButtonID == t.ButtonID {
				return true
			}
		case TriggerKeyword:
			for _, k := range t.Keywords {
				k = strings.ToLower(strings.TrimSpace(k))
				if (t.Match == "contains" && text != "" && strings.Contains(text, k)) || (t.Match != "contains" && text == k) {
					return true
				}
			}
		}
	}
	return false
}
